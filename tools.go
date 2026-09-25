package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Arguments struct {
	Text        string `json:"text,omitempty"`
	PostID      int64  `json:"post_id,omitempty"`
	CommentID   int64  `json:"comment_id,omitempty"`
	PeerID      int64  `json:"peer_id,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	Asset       string `json:"asset,omitempty"`
	Attachments string `json:"attachments,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Count       int    `json:"count,omitempty"`
}
type Intent struct {
	Tool      string    `json:"tool"`
	Arguments Arguments `json:"arguments"`
}
type Action struct {
	ID, Tool, Args, Summary, Risk, Status, Result, Error string
	ApprovedBy, Expires                                  int64
	Automatic                                            bool
}

var toolsAllowed = map[string]string{
	"vk.get_wall": "wall.get", "vk.create_post": "wall.post", "vk.edit_post": "wall.edit", "vk.delete_post": "wall.delete",
	"vk.get_comments": "wall.getComments", "vk.reply_comment": "wall.createComment", "vk.delete_comment": "wall.deleteComment",
	"vk.get_messages": "messages.getHistory", "vk.send_message": "messages.send", "vk.upload_photo": "photos.getWallUploadServer",
	"vk.get_members": "groups.getMembers", "vk.get_statistics": "stats.get", "vk.get_community_info": "groups.getById",
	"vk.ban_member": "groups.ban", "vk.unban_member": "groups.unban", "vk.get_content_stats": "stats.getPostReach",
	"vk.get_recent_activity": "composite",
}
var photoPattern = regexp.MustCompile(`^photo-?[0-9]+_[0-9]+(,photo-?[0-9]+_[0-9]+){0,3}$`)

func isWrite(tool string) bool { return !strings.HasPrefix(tool, "vk.get_") }
func validateIntent(i Intent) error {
	if _, ok := toolsAllowed[i.Tool]; !ok {
		return errors.New("tool is not allowed; administrative rights/settings tools are CRITICAL and disabled")
	}
	a := i.Arguments
	if len([]rune(a.Text)) > 3500 || len(a.Reason) > 500 || a.Count < 0 || a.Count > 50 {
		return errors.New("argument limit exceeded")
	}
	if a.Attachments != "" && !photoPattern.MatchString(a.Attachments) {
		return errors.New("invalid photo attachments")
	}
	switch i.Tool {
	case "vk.create_post":
		if strings.TrimSpace(a.Text) == "" {
			return errors.New("post text required")
		}
	case "vk.edit_post":
		if a.PostID <= 0 || a.Text == "" {
			return errors.New("post_id and text required")
		}
	case "vk.delete_post", "vk.get_comments", "vk.get_content_stats":
		if a.PostID <= 0 {
			return errors.New("post_id required")
		}
	case "vk.reply_comment":
		if a.PostID <= 0 || a.CommentID <= 0 || a.Text == "" {
			return errors.New("post_id, comment_id and text required")
		}
	case "vk.delete_comment":
		if a.CommentID <= 0 || a.Reason == "" {
			return errors.New("comment_id and reason required")
		}
	case "vk.send_message":
		if a.PeerID <= 0 || a.Text == "" {
			return errors.New("peer_id and text required")
		}
	case "vk.get_messages":
		if a.PeerID <= 0 {
			return errors.New("peer_id required")
		}
	case "vk.ban_member", "vk.unban_member":
		if a.UserID <= 0 || a.Reason == "" {
			return errors.New("user_id and reason required")
		}
	case "vk.upload_photo":
		if a.Asset == "" || strings.ContainsAny(a.Asset, "/\\") || a.Asset == "." {
			return errors.New("asset basename required")
		}
	}
	return nil
}
func (e *Engine) risk(i Intent) string {
	switch i.Tool {
	case "vk.delete_post", "vk.ban_member", "vk.unban_member":
		return "HIGH"
	case "vk.edit_post":
		return "HIGH" // Cannot prove freshness + low reach: fail closed.
	case "vk.delete_comment":
		// A model's assertion of spam is not evidence. Only exact backend blacklist matches qualify.
		if e.s.Count("SELECT COUNT(*) FROM events WHERE id=? AND payload LIKE ?", "spam:"+number(i.Arguments.CommentID), "%") > 0 {
			return "MEDIUM"
		}
		return "HIGH"
	case "vk.create_post", "vk.reply_comment", "vk.upload_photo":
		return "MEDIUM"
	default:
		return "LOW"
	}
}
func (e *Engine) action(id string) (a Action, err error) {
	err = e.s.QueryRow("SELECT id,tool,args,summary,risk,status,result,error,approved_by,expires,automatic FROM actions WHERE id=?", id).Scan(&a.ID, &a.Tool, &a.Args, &a.Summary, &a.Risk, &a.Status, &a.Result, &a.Error, &a.ApprovedBy, &a.Expires, &a.Automatic)
	return
}
func (e *Engine) submit(i Intent, dedupe string, automatic bool) (Action, error) {
	if err := validateIntent(i); err != nil {
		return Action{}, err
	}
	b, _ := json.Marshal(i.Arguments)
	if e.redact.Clean(string(b)) != string(b) {
		return Action{}, errors.New("secret-like data rejected")
	}
	if i.Tool == "vk.ban_member" && i.Arguments.UserID == e.cfg.BossID {
		return Action{}, errors.New("boss cannot be moderated")
	}
	if (i.Tool == "vk.send_message" || i.Tool == "vk.get_messages") && i.Arguments.PeerID != e.cfg.BossID && e.s.Count("SELECT COUNT(*) FROM users WHERE id=?", i.Arguments.PeerID) == 0 {
		return Action{}, errors.New("unknown recipient; inbound context required")
	}
	risk := e.risk(i)
	state := "QUEUED"
	if risk == "HIGH" {
		state = "PENDING"
	}
	if automatic && !e.autonomous() {
		return Action{}, errors.New("autonomy disabled or paused")
	}
	now := time.Now()
	day := now.In(e.cfg.Location).Format("2006-01-02")
	if i.Tool == "vk.create_post" && e.s.Count("SELECT COUNT(*) FROM actions WHERE tool='vk.create_post' AND day=? AND status NOT IN ('REJECTED','CANCELED','FAILED')", day) >= e.cfg.MaxPosts {
		risk = "HIGH"
		state = "PENDING"
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return Action{}, err
	}
	id := "ACTION-" + strings.ToUpper(hex.EncodeToString(buf))
	summary := clip(i.Tool+" "+string(b), 700)
	_, err := e.s.Exec("INSERT OR IGNORE INTO actions(id,created,agent,tool,args,summary,risk,status,expires,dedupe,automatic,day) VALUES(?,?,'anna',?,?,?,?,?,?,?,?,?)", id, now.Unix(), i.Tool, string(b), summary, risk, state, now.Add(24*time.Hour).Unix(), dedupe, automatic, day)
	if err != nil {
		return Action{}, err
	}
	var actual string
	if err = e.s.QueryRow("SELECT id FROM actions WHERE dedupe=?", dedupe).Scan(&actual); err != nil {
		return Action{}, err
	}
	a, err := e.action(actual)
	if err != nil {
		return a, err
	}
	if a.Status == "QUEUED" {
		err = e.s.Enqueue("action", map[string]string{"id": a.ID}, "action:"+a.ID, automatic, now.Unix())
	}
	if a.Status == "PENDING" && actual == id {
		e.notify("Данил, требуется подтверждение " + id + "\n" + summary + "\n/approve " + id + "\n/reject " + id)
	}
	return a, err
}
func (e *Engine) approve(id string, boss int64, yes bool) error {
	if boss != e.cfg.BossID {
		return errors.New("only Danil may approve")
	}
	e.gate.Lock()
	defer e.gate.Unlock()
	if e.s.Get("paused") == "true" && yes {
		return errors.New("paused; resume before approval")
	}
	tx, err := e.s.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state := "REJECTED"
	if yes {
		state = "QUEUED"
	}
	r, err := tx.Exec("UPDATE actions SET status=?,approved_by=?,automatic=0 WHERE id=? AND status='PENDING' AND expires>?", state, boss, id, time.Now().Unix())
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return errors.New("action absent, expired or already decided")
	}
	if yes {
		b, _ := json.Marshal(map[string]string{"id": id})
		due := time.Now().Unix()
		var planned int64
		tx.QueryRow("SELECT COALESCE(MAX(planned),0) FROM content WHERE action_id=?", id).Scan(&planned)
		if planned > due {
			due = planned
		}
		_, err = tx.Exec("INSERT INTO jobs(kind,payload,created,due,dedupe,automatic) VALUES('action',?,?,?,?,0) ON CONFLICT(dedupe) DO UPDATE SET status='QUEUED',automatic=0,due=excluded.due,attempts=0", string(b), time.Now().Unix(), due, "action:"+id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (e *Engine) execute(ctx context.Context, id string) (err error) {
	e.gate.Lock()
	a, err := e.action(id)
	if err != nil {
		e.gate.Unlock()
		return err
	}
	if a.Status != "QUEUED" {
		e.gate.Unlock()
		return nil
	}
	var args Arguments
	if json.Unmarshal([]byte(a.Args), &args) != nil {
		e.gate.Unlock()
		return errors.New("corrupt stored action")
	}
	err = e.admit(a, args)
	if err != nil {
		var approval *needsApproval
		if errors.As(err, &approval) {
			e.s.Exec("UPDATE actions SET status='PENDING',risk='HIGH',approved_by=0,error=? WHERE id=?", err.Error(), id)
			e.gate.Unlock()
			e.notify("Нужно подтверждение: " + id + "\n" + err.Error() + "\n/approve " + id + "\n/reject " + id)
			return err
		}
		e.s.Exec("UPDATE actions SET status='CANCELED',error=? WHERE id=?", err.Error(), id)
		e.gate.Unlock()
		return err
	}
	if ctx.Err() != nil {
		e.gate.Unlock()
		return ctx.Err()
	}
	_, err = e.s.Exec("UPDATE actions SET status='EXECUTING',day=? WHERE id=? AND status='QUEUED'", time.Now().In(e.cfg.Location).Format("2006-01-02"), id)
	e.gate.Unlock()
	if err != nil {
		return err
	}
	result, callErr := e.invoke(ctx, a, args)
	status := "SUCCEEDED"
	msg := ""
	if callErr != nil {
		msg = e.redact.Clean(callErr.Error())
		status = "FAILED"
		var api *APIError
		if isWrite(a.Tool) && !errors.As(callErr, &api) {
			status = "UNKNOWN"
		}
		var critical *criticalError
		if errors.As(callErr, &critical) {
			status = "REJECTED"
			e.s.Exec("UPDATE actions SET risk='CRITICAL' WHERE id=?", id)
		}
	}
	_, err = e.s.Exec("UPDATE actions SET status=?,result=?,error=? WHERE id=?", status, clip(e.redact.Clean(string(result)), 12000), msg, id)
	if err != nil {
		return err
	}
	if a.Tool == "vk.create_post" {
		e.s.Exec("UPDATE content SET status=?,result=? WHERE action_id=?", map[bool]string{true: "PUBLISHED", false: "FAILED"}[status == "SUCCEEDED"], clip(string(result)+msg, 1500), id)
	}
	if callErr != nil {
		e.notify(id + ": " + msg + ". Автоматически не повторяю; при UNKNOWN проверь действие в VK.")
		return callErr
	}
	e.success(a, result)
	return nil
}
func (e *Engine) admit(a Action, args Arguments) error {
	if e.s.Get("paused") == "true" {
		return errors.New("queue paused")
	}
	if a.Automatic && !e.autonomous() {
		return errors.New("autonomy disabled")
	}
	if a.Risk == "HIGH" && a.ApprovedBy != e.cfg.BossID {
		return errors.New("approval required")
	}
	if a.Risk == "HIGH" && a.Expires < time.Now().Unix() {
		return errors.New("approval expired before execution")
	}
	if a.Tool == "vk.reply_comment" && args.UserID > 0 && e.s.Count("SELECT COUNT(*) FROM users WHERE id=? AND (handoff=1 OR blacklisted=1)", args.UserID) > 0 {
		return errors.New("human handoff or blacklist")
	}
	if a.Risk == "CRITICAL" {
		return errors.New("critical action forbidden")
	}
	if a.Tool == "vk.create_post" && e.s.Get("no_publish_date") == time.Now().In(e.cfg.Location).Format("2006-01-02") {
		return errors.New("boss forbids publishing today")
	}
	if a.Tool == "vk.create_post" && a.ApprovedBy != e.cfg.BossID {
		day := time.Now().In(e.cfg.Location).Format("2006-01-02")
		if e.s.Count("SELECT COUNT(*) FROM actions WHERE tool='vk.create_post' AND day=? AND status IN ('SUCCEEDED','EXECUTING','UNKNOWN')", day) >= e.cfg.MaxPosts {
			return &needsApproval{"Дневной лимит публикаций: требуется решение Данила"}
		}
	}
	if a.Tool == "vk.send_message" && args.PeerID != e.cfg.BossID {
		if e.s.Count("SELECT COUNT(*) FROM users WHERE id=? AND (handoff=1 OR blacklisted=1)", args.PeerID) > 0 {
			return errors.New("human handoff or blacklist")
		}
	}
	if a.Automatic && (a.Tool == "vk.send_message" || a.Tool == "vk.reply_comment") {
		day := time.Now().In(e.cfg.Location).Format("2006-01-02")
		if e.s.Count("SELECT COUNT(*) FROM actions WHERE automatic=1 AND day=? AND tool IN ('vk.send_message','vk.reply_comment') AND status IN ('SUCCEEDED','EXECUTING','UNKNOWN')", day) >= e.cfg.MaxDaily {
			return errors.New("daily autonomous message limit")
		}
	}
	return nil
}
func (e *Engine) invoke(ctx context.Context, a Action, x Arguments) (json.RawMessage, error) {
	count := x.Count
	if count == 0 {
		count = 20
	}
	p := url.Values{}
	method := toolsAllowed[a.Tool]
	switch a.Tool {
	case "vk.get_wall":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "count": fmt.Sprint(count), "filter": "owner"})
	case "vk.create_post", "vk.edit_post":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "message": x.Text, "attachments": x.Attachments})
		if a.Tool == "vk.create_post" {
			p.Set("from_group", "1")
			p.Set("guid", a.ID)
		} else {
			p.Set("post_id", number(x.PostID))
		}
	case "vk.delete_post":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "post_id": number(x.PostID)})
	case "vk.get_comments":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "post_id": number(x.PostID), "count": fmt.Sprint(count)})
	case "vk.reply_comment":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "post_id": number(x.PostID), "reply_to_comment": number(x.CommentID), "message": x.Text, "from_group": number(e.cfg.GroupID), "guid": a.ID})
	case "vk.delete_comment":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "comment_id": number(x.CommentID)})
	case "vk.get_messages":
		p = values(map[string]string{"peer_id": number(x.PeerID), "count": fmt.Sprint(count)})
	case "vk.send_message":
		p = values(map[string]string{"peer_id": number(x.PeerID), "message": x.Text, "random_id": stableRandom(a.ID)})
	case "vk.upload_photo":
		if u, ok := e.vk.(interface {
			Upload(context.Context, string) (json.RawMessage, error)
		}); ok {
			return u.Upload(ctx, x.Asset)
		}
		return nil, errors.New("upload transport unavailable")
	case "vk.get_members":
		p = values(map[string]string{"group_id": number(e.cfg.GroupID), "count": fmt.Sprint(count)})
	case "vk.get_statistics":
		p = values(map[string]string{"group_id": number(e.cfg.GroupID), "timestamp_from": number(time.Now().Add(-7 * 24 * time.Hour).Unix()), "timestamp_to": number(time.Now().Unix()), "interval": "day"})
	case "vk.get_community_info":
		p = values(map[string]string{"group_ids": number(e.cfg.GroupID), "fields": "description,members_count,status"})
	case "vk.ban_member", "vk.unban_member":
		if err := e.protectManagers(ctx, x.UserID, a.ID); err != nil {
			return nil, err
		}
		p = values(map[string]string{"group_id": number(e.cfg.GroupID), "owner_id": number(x.UserID)})
		if a.Tool == "vk.ban_member" {
			p.Set("comment", x.Reason)
			p.Set("end_date", number(time.Now().Add(24*time.Hour).Unix()))
		}
	case "vk.get_content_stats":
		p = values(map[string]string{"owner_id": number(-e.cfg.GroupID), "post_ids": number(x.PostID)})
	case "vk.get_recent_activity":
		return json.Marshal(map[string]any{"scope": "locally received Long Poll events, last 24h", "events": e.s.Count("SELECT COUNT(*) FROM events WHERE created>?", time.Now().Add(-24*time.Hour).Unix()), "actions": e.s.Count("SELECT COUNT(*) FROM actions WHERE created>?", time.Now().Add(-24*time.Hour).Unix())})
	}
	return e.vk.Call(ctx, method, p)
}
func (e *Engine) readTool(ctx context.Context, i Intent, key string) (string, error) {
	a, err := e.submit(i, key, false)
	if err != nil {
		return "", err
	}
	if a.Status == "QUEUED" {
		err = e.execute(ctx, a.ID)
		if err != nil {
			return "", err
		}
	}
	a, err = e.action(a.ID)
	if err != nil {
		return "", err
	}
	if a.Status != "SUCCEEDED" {
		return "", errors.New("read unavailable")
	}
	return a.Result, nil
}
