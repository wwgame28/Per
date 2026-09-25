package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"os"
	"path/filepath"
	"time"
)

type Store struct{ *sql.DB }

func openStore(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=FULL")
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	_, e = db.Exec(schema)
	if e != nil {
		db.Close()
		return nil, e
	}
	os.Chmod(path, 0600)
	// A network request may have succeeded before a crash. Never replay it blindly.
	_, e = db.Exec(`UPDATE actions SET status='UNKNOWN',error='Interrupted: verify remotely before retry' WHERE status='EXECUTING'; UPDATE jobs SET status='QUEUED' WHERE status='RUNNING';`)
	return s, e
}

const schema = `
PRAGMA max_page_count=65536;
PRAGMA cache_size=-2048;
CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS memory(agent TEXT,key TEXT,value TEXT,PRIMARY KEY(agent,key));
CREATE TABLE IF NOT EXISTS actions(
 id TEXT PRIMARY KEY,created INTEGER NOT NULL,agent TEXT NOT NULL,tool TEXT NOT NULL,args TEXT NOT NULL,
 summary TEXT NOT NULL,risk TEXT NOT NULL,approved_by INTEGER NOT NULL DEFAULT 0,status TEXT NOT NULL,
 result TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',expires INTEGER NOT NULL,
 dedupe TEXT UNIQUE,automatic INTEGER NOT NULL,day TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS action_status ON actions(status,created);
CREATE TABLE IF NOT EXISTS jobs(id INTEGER PRIMARY KEY AUTOINCREMENT,kind TEXT,payload TEXT,status TEXT DEFAULT 'QUEUED',
 created INTEGER,due INTEGER,attempts INTEGER DEFAULT 0,error TEXT DEFAULT '',dedupe TEXT UNIQUE,automatic INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS job_due ON jobs(status,due);
CREATE TABLE IF NOT EXISTS content(id INTEGER PRIMARY KEY AUTOINCREMENT,idea TEXT,goal TEXT DEFAULT '',status TEXT DEFAULT 'IDEA',
 responsible TEXT DEFAULT 'anna',planned INTEGER DEFAULT 0,draft TEXT DEFAULT '',visual TEXT DEFAULT '',review TEXT DEFAULT '',
 action_id TEXT DEFAULT '',result TEXT DEFAULT '',created INTEGER);
CREATE TABLE IF NOT EXISTS dialogue(id INTEGER PRIMARY KEY AUTOINCREMENT,created INTEGER,content_id INTEGER,agent TEXT,text TEXT);
CREATE TABLE IF NOT EXISTS events(id TEXT PRIMARY KEY,created INTEGER,payload TEXT,state TEXT DEFAULT 'NEW');
CREATE TABLE IF NOT EXISTS users(id INTEGER PRIMARY KEY,blacklisted INTEGER DEFAULT 0,handoff INTEGER DEFAULT 0,last_seen INTEGER DEFAULT 0,
 last_reply INTEGER DEFAULT 0,last_hash TEXT DEFAULT '',window_start INTEGER DEFAULT 0,window_count INTEGER DEFAULT 0);
`

func (s *Store) Get(k string) string {
	var v string
	s.QueryRow("SELECT value FROM settings WHERE key=?", k).Scan(&v)
	return v
}
func (s *Store) Set(k, v string) error {
	_, e := s.Exec("INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v)
	return e
}
func (s *Store) Memory(agent, key string) string {
	var v string
	s.QueryRow("SELECT value FROM memory WHERE agent=? AND key=?", agent, key).Scan(&v)
	return v
}
func (s *Store) Remember(agent, key, value string) error {
	_, e := s.Exec("INSERT INTO memory VALUES(?,?,?) ON CONFLICT(agent,key) DO UPDATE SET value=excluded.value", agent, key, clip(value, 1500))
	return e
}
func (s *Store) Enqueue(kind string, payload any, dedupe string, automatic bool, due int64) error {
	b, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	_, e = s.Exec("INSERT OR IGNORE INTO jobs(kind,payload,created,due,dedupe,automatic) VALUES(?,?,?,?,?,?)", kind, string(b), time.Now().Unix(), due, dedupe, automatic)
	return e
}

type Job struct {
	ID            int64
	Kind, Payload string
	Automatic     bool
	Attempts      int
}

func (s *Store) Claim() (j Job, e error) {
	tx, e := s.Begin()
	if e != nil {
		return j, e
	}
	defer tx.Rollback()
	e = tx.QueryRow("SELECT id,kind,payload,automatic,attempts FROM jobs WHERE status='QUEUED' AND due<=? ORDER BY automatic,id LIMIT 1", time.Now().Unix()).Scan(&j.ID, &j.Kind, &j.Payload, &j.Automatic, &j.Attempts)
	if e != nil {
		return j, e
	}
	_, e = tx.Exec("UPDATE jobs SET status='RUNNING',attempts=attempts+1 WHERE id=?", j.ID)
	if e != nil {
		return j, e
	}
	e = tx.Commit()
	return
}
func (s *Store) Count(query string, args ...any) int {
	var n int
	s.QueryRow(query, args...).Scan(&n)
	return n
}
func (s *Store) Dump(query string, args ...any) string {
	rows, e := s.Query(query, args...)
	if e != nil {
		return "Ошибка чтения"
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := ""
	for rows.Next() {
		vs := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range vs {
			ptr[i] = &vs[i]
		}
		if rows.Scan(ptr...) != nil {
			break
		}
		for i, v := range vs {
			if i > 0 {
				out += " | "
			}
			out += fmt.Sprint(v)
		}
		out += "\n"
	}
	if out == "" {
		return "Пока пусто."
	}
	return clip(out, 3500)
}
