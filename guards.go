package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type needsApproval struct{ message string }

func (e *needsApproval) Error() string { return e.message }

type criticalError struct{ message string }

func (e *criticalError) Error() string { return e.message }

var priceQuestion = regexp.MustCompile(`(?i)(сколько|цен[аыу]|стоимост|прайс)`)
var priceLine = regexp.MustCompile(`(?i)[0-9][0-9 .,]*(₽|руб|р\.|rur|rub)`)

func verifiedPrice(knowledge string) string {
	var lines []string
	for _, line := range strings.Split(knowledge, "\n") {
		if priceLine.MatchString(line) {
			lines = append(lines, line)
		}
	}
	return clip(strings.Join(lines, "\n"), 1000)
}

// An approved ban is still forbidden if the target is an owner/administrator.
// Fresh VK evidence is mandatory; permission errors fail closed.
func (e *Engine) protectManagers(ctx context.Context, user int64, key string) error {
	if user == e.cfg.BossID {
		return &criticalError{"CRITICAL: boss cannot be moderated"}
	}
	raw, err := e.vk.Call(ctx, "groups.getMembers", values(map[string]string{"group_id": number(e.cfg.GroupID), "filter": "managers", "count": "1000"}))
	if err != nil {
		return err
	}
	var r struct {
		Count int `json:"count"`
		Items []struct {
			ID   int64  `json:"id"`
			Role string `json:"role"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Count < 1 || r.Count != len(r.Items) {
		return errors.New("manager list incomplete; ban blocked")
	}
	e.recordRead("guard:"+key, "groups.getMembers", fmt.Sprint("manager check for ", user), raw)
	for _, m := range r.Items {
		if m.ID == user {
			return &criticalError{"CRITICAL: community manager cannot be moderated"}
		}
	}
	return nil
}
func (e *Engine) recordRead(id, method, summary string, result json.RawMessage) {
	now := time.Now()
	e.s.Exec("INSERT OR IGNORE INTO actions(id,created,agent,tool,args,summary,risk,status,result,expires,dedupe,automatic,day) VALUES(?,?,'backend',?,'{}',?,'LOW','SUCCEEDED',?,?,?,0,?)", id, now.Unix(), method, summary, clip(e.redact.Clean(string(result)), 8000), now.Add(time.Hour).Unix(), id, now.In(e.cfg.Location).Format("2006-01-02"))
}
func (e *Engine) reconcile() error {
	// Repair a crash between action persistence, content linkage and queue insertion.
	_, err := e.s.Exec(`
UPDATE actions SET status='EXPIRED' WHERE status='PENDING' AND expires<unixepoch();
UPDATE content SET action_id=(SELECT id FROM actions WHERE dedupe='publish:'||content.id)
 WHERE action_id='' AND EXISTS(SELECT 1 FROM actions WHERE dedupe='publish:'||content.id);
UPDATE content SET status=CASE
 WHEN (SELECT status FROM actions WHERE id=content.action_id)='SUCCEEDED' THEN 'PUBLISHED'
 WHEN (SELECT status FROM actions WHERE id=content.action_id) IN ('UNKNOWN','FAILED','REJECTED','CANCELED','EXPIRED') THEN 'FAILED'
 ELSE 'SCHEDULED' END,
 result=COALESCE((SELECT CASE WHEN error!='' THEN error ELSE result END FROM actions WHERE id=content.action_id),'')
 WHERE action_id!='';
INSERT OR IGNORE INTO jobs(kind,payload,created,due,dedupe,automatic)
 SELECT 'action',json_object('id',a.id),a.created,
 MAX(unixepoch(),COALESCE((SELECT planned FROM content WHERE action_id=a.id),0)),
 'action:'||a.id,a.automatic FROM actions a WHERE a.status='QUEUED';
UPDATE jobs SET due=MAX(due,COALESCE((SELECT planned FROM content WHERE 'action:'||action_id=jobs.dedupe),0)) WHERE kind='action' AND status='QUEUED';
`)
	return err
}
func (e *Engine) housekeeping() {
	// Hard cap is set by SQLite; retention keeps routine logs bounded well below it.
	e.s.Exec(`DELETE FROM dialogue WHERE created<unixepoch()-2592000;
 DELETE FROM events WHERE state!='NEW' AND created<unixepoch()-2592000;
 DELETE FROM jobs WHERE status IN ('DONE','FAILED','CANCELED') AND created<unixepoch()-7776000;
 DELETE FROM actions WHERE status NOT IN ('PENDING','QUEUED','EXECUTING','UNKNOWN') AND created<unixepoch()-7776000;
 DELETE FROM users WHERE last_seen<unixepoch()-7776000 AND handoff=0 AND blacklisted=0;
 DELETE FROM memory WHERE agent LIKE 'user:%' AND CAST(substr(agent,6) AS INTEGER) NOT IN (SELECT id FROM users);
 `)
	e.reconcile()
}
func (e *Engine) prohibitPublication() error {
	e.gate.Lock()
	defer e.gate.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	tx, err := e.s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	day := time.Now().In(e.cfg.Location).Format("2006-01-02")
	statements := []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO settings VALUES('no_publish_date',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", []any{day}},
		{"UPDATE actions SET status='CANCELED',error='Boss publication veto' WHERE tool='vk.create_post' AND status IN ('QUEUED','PENDING')", nil},
		{"UPDATE jobs SET status='CANCELED',error='Boss publication veto' WHERE kind='content' AND status IN ('QUEUED','RUNNING')", nil},
		{"UPDATE content SET status='FAILED',result='Boss publication veto' WHERE status NOT IN ('PUBLISHED','FAILED')", nil},
	}
	for _, s := range statements {
		if _, err = tx.Exec(s.sql, s.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (e *Engine) bossMessage(ctx context.Context, text string) {
	id := fmt.Sprint("CONTROL-", time.Now().UnixNano())
	now := time.Now()
	text = e.redact.Clean(text)
	_, err := e.s.Exec("INSERT INTO actions(id,created,agent,tool,args,summary,risk,status,expires,dedupe,automatic,day) VALUES(?,?,'control','vk.send_message','{}',?,'LOW','EXECUTING',?,?,0,?)", id, now.Unix(), clip(text, 500), now.Add(time.Hour).Unix(), id, now.In(e.cfg.Location).Format("2006-01-02"))
	if err != nil {
		return
	}
	result, err := e.vk.Call(ctx, "messages.send", values(map[string]string{"peer_id": number(e.cfg.BossID), "message": text, "random_id": stableRandom(id)}))
	state := "SUCCEEDED"
	msg := ""
	if err != nil {
		state = "UNKNOWN"
		msg = e.redact.Clean(err.Error())
		e.s.Set("notification_error", "VK notification failed; inspect /anna log")
	}
	e.s.Exec("UPDATE actions SET status=?,result=?,error=? WHERE id=?", state, clip(e.redact.Clean(string(result)), 1000), msg, id)
}
func (e *Engine) success(a Action, result json.RawMessage) {
	e.s.Remember("anna", "recent_decisions", a.ID+" "+a.Tool+" SUCCEEDED "+clip(e.redact.Clean(string(result)), 500))
	if a.Tool == "vk.create_post" {
		var p struct {
			ID int64 `json:"post_id"`
		}
		if json.Unmarshal(result, &p) == nil && p.ID > 0 {
			e.notify("Опубликовано: https://vk.com/wall-" + number(e.cfg.GroupID) + "_" + number(p.ID) + "\n" + a.ID)
			return
		}
	}
	if !a.Automatic && !strings.HasPrefix(a.ID, "CONTROL-") {
		e.notify(a.ID + " выполнено: " + clip(e.redact.Clean(string(result)), 1000))
	}
}
