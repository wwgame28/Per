package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

type managersVK struct{ calledBan bool }

func (v *managersVK) Call(_ context.Context, method string, _ url.Values) (json.RawMessage, error) {
	if method == "groups.getMembers" {
		return json.RawMessage(`{"count":2,"items":[{"id":1,"role":"creator"},{"id":2,"role":"administrator"}]}`), nil
	}
	if method == "groups.ban" {
		v.calledBan = true
	}
	return json.RawMessage(`1`), nil
}
func TestManagerBanBlockedEvenAfterApproval(t *testing.T) {
	e, _, _ := fixture(t)
	v := &managersVK{}
	e.vk = v
	a, err := e.submit(Intent{Tool: "vk.ban_member", Arguments: Arguments{UserID: 2, Reason: "spam"}}, "manager", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.approve(a.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err = e.execute(context.Background(), a.ID); err == nil {
		t.Fatal("manager ban allowed")
	}
	if v.calledBan {
		t.Fatal("ban API called")
	}
}
func TestMissingPermissionsFailClosed(t *testing.T) {
	e, v, _ := fixture(t)
	v.err = &APIError{27}
	a, _ := e.submit(Intent{Tool: "vk.ban_member", Arguments: Arguments{UserID: 2, Reason: "spam"}}, "noperms", true)
	e.approve(a.ID, 1, true)
	if e.execute(context.Background(), a.ID) == nil {
		t.Fatal("missing permission ignored")
	}
	for _, m := range v.calls {
		if m == "groups.ban" {
			t.Fatal("ban despite failed preflight")
		}
	}
}
func TestReconcileOrphanActionAndSchedule(t *testing.T) {
	e, _, _ := fixture(t)
	future := time.Now().Add(time.Hour).Unix()
	id, _ := e.newContent("idea", false, future)
	a, _ := e.submit(Intent{Tool: "vk.create_post", Arguments: Arguments{Text: "post"}}, "publish:"+number(id), false)
	e.s.Exec("DELETE FROM jobs WHERE dedupe=?", "action:"+a.ID)
	if err := e.reconcile(); err != nil {
		t.Fatal(err)
	}
	var due int64
	var link string
	e.s.QueryRow("SELECT action_id FROM content WHERE id=?", id).Scan(&link)
	e.s.QueryRow("SELECT due FROM jobs WHERE dedupe=?", "action:"+a.ID).Scan(&due)
	if due != future || link != a.ID {
		t.Fatal(due, link)
	}
}
func TestApprovedScheduledActionRetainsDate(t *testing.T) {
	e, _, _ := fixture(t)
	future := time.Now().Add(time.Hour).Unix()
	id, _ := e.newContent("idea", false, future)
	a, _ := e.submit(Intent{Tool: "vk.delete_post", Arguments: Arguments{PostID: 7}}, "scheduledhigh", false)
	e.s.Exec("UPDATE content SET action_id=? WHERE id=?", a.ID, id)
	if err := e.approve(a.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	var due int64
	e.s.QueryRow("SELECT due FROM jobs WHERE dedupe=?", "action:"+a.ID).Scan(&due)
	if due != future {
		t.Fatal(due)
	}
}
func TestDailyMessageLimitAndReport(t *testing.T) {
	e, v, _ := fixture(t)
	e.cfg.MaxDaily = 1
	e.s.Exec("INSERT INTO users(id) VALUES(2)")
	i := Intent{Tool: "vk.send_message", Arguments: Arguments{PeerID: 2, Text: "hi"}}
	a, _ := e.submit(i, "limit1", true)
	e.execute(context.Background(), a.ID)
	b, _ := e.submit(i, "limit2", true)
	if e.execute(context.Background(), b.ID) == nil {
		t.Fatal("limit bypass")
	}
	if len(v.calls) != 1 {
		t.Fatal(v.calls)
	}
	if !strings.Contains(e.report(), "Сообщений: 1") {
		t.Fatal(e.report())
	}
}
func TestHandoffCancelsQueuedComment(t *testing.T) {
	e, v, _ := fixture(t)
	e.s.Exec("INSERT INTO users(id,handoff) VALUES(2,1)")
	a, _ := e.submit(Intent{Tool: "vk.reply_comment", Arguments: Arguments{PostID: 3, CommentID: 4, UserID: 2, Text: "hello"}}, "handoffcomment", true)
	if e.execute(context.Background(), a.ID) == nil {
		t.Fatal("handoff ignored")
	}
	if len(v.calls) != 0 {
		t.Fatal(v.calls)
	}
}
func TestUploadAndCommandValidation(t *testing.T) {
	for _, i := range []Intent{{Tool: "vk.upload_photo", Arguments: Arguments{Asset: "../secret.env"}}, {Tool: "vk.create_post", Arguments: Arguments{Text: "x", Attachments: "https://evil.test/a"}}, {Tool: "vk.get_members", Arguments: Arguments{Count: 100000}}} {
		if validateIntent(i) == nil {
			t.Fatal("accepted", i)
		}
	}
	var i Intent
	if strictJSON(`{"tool":"vk.create_post","arguments":{"text":"x","owner_id":-42}}`, &i) == nil {
		t.Fatal("scope override accepted")
	}
}

type blockingLLM struct{ started chan struct{} }

func (l *blockingLLM) Ask(ctx context.Context, _ string, _ string, _ bool) (string, error) {
	select {
	case l.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return "", ctx.Err()
}
func TestEmergencyStopInterruptsRealWorker(t *testing.T) {
	e, v, _ := fixture(t)
	l := &blockingLLM{started: make(chan struct{}, 1)}
	e.llm = l
	id, _ := e.newContent("idea", true, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-l.started:
	case <-time.After(3 * time.Second):
		t.Fatal("worker not started")
	}
	e.command(Event{UserID: 1, PeerID: 1, Text: "/anna emergency-stop"})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var state string
		e.s.QueryRow("SELECT status FROM jobs WHERE dedupe=?", "content:"+number(id)).Scan(&state)
		if state == "QUEUED" {
			v.mu.Lock()
			n := len(v.calls)
			v.mu.Unlock()
			if n != 0 {
				t.Fatal("VK action after stop")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("interrupted job was not persisted")
}
func TestNormalizeAndGroupMessageSafety(t *testing.T) {
	x, ok := normalize(pollEvent{Type: "message_new", Object: json.RawMessage(`{"message":{"id":8,"from_id":2,"peer_id":2,"text":"hello","out":0}}`)})
	if !ok || x.UserID != 2 {
		t.Fatal(x)
	}
	e, _, _ := fixture(t)
	x.PeerID = 2000000001
	if e.accept(x) {
		t.Fatal("group chat treated as DM")
	}
	_, ok = normalize(pollEvent{Type: "message_new", Object: json.RawMessage(`{"message":{"from_id":2,"peer_id":2,"out":1}}`)})
	if ok {
		t.Fatal("bot output loop")
	}
}
func TestUncertainActionNeverRequeuedByReconcile(t *testing.T) {
	e, v, _ := fixture(t)
	v.err = errors.New("network timeout")
	a, _ := e.submit(Intent{Tool: "vk.create_post", Arguments: Arguments{Text: "post"}}, "unknown-recovery", true)
	e.execute(context.Background(), a.ID)
	e.s.Exec("DELETE FROM jobs WHERE dedupe=?", "action:"+a.ID)
	e.reconcile()
	if e.s.Count("SELECT COUNT(*) FROM jobs WHERE dedupe=?", "action:"+a.ID) != 0 {
		t.Fatal("uncertain write replayed")
	}
}

func TestMissingPriceNeverInvented(t *testing.T) {
	e, _, _ := fixture(t)
	e.s.Remember("anna", "knowledge", "Мы ремонтируем велосипеды.")
	x := Event{ID: "price", Kind: "message", PeerID: 2, UserID: 2, Text: "Сколько стоит?"}
	e.accept(x)
	if err := e.reply(context.Background(), x, true); err != nil {
		t.Fatal(err)
	}
	var args string
	e.s.QueryRow("SELECT args FROM actions WHERE dedupe=?", "response:price").Scan(&args)
	if !strings.Contains(args, "подтверждённого прайса") {
		t.Fatal(args)
	}
	if verifiedPrice("Консультация — 500 рублей.") != "Консультация — 500 рублей." {
		t.Fatal("known price missing")
	}
}

func TestEveryVKToolMapsToExpectedMethod(t *testing.T) {
	fixtures := []struct {
		tool, method string
		args         Arguments
	}{
		{"vk.get_wall", "wall.get", Arguments{}}, {"vk.create_post", "wall.post", Arguments{Text: "post"}},
		{"vk.edit_post", "wall.edit", Arguments{PostID: 3, Text: "edit"}}, {"vk.delete_post", "wall.delete", Arguments{PostID: 3}},
		{"vk.get_comments", "wall.getComments", Arguments{PostID: 3}}, {"vk.reply_comment", "wall.createComment", Arguments{PostID: 3, CommentID: 4, Text: "reply"}},
		{"vk.delete_comment", "wall.deleteComment", Arguments{CommentID: 4, Reason: "spam"}},
		{"vk.get_messages", "messages.getHistory", Arguments{PeerID: 1}}, {"vk.send_message", "messages.send", Arguments{PeerID: 1, Text: "hi"}},
		{"vk.get_members", "groups.getMembers", Arguments{}}, {"vk.get_statistics", "stats.get", Arguments{}}, {"vk.get_community_info", "groups.getById", Arguments{}},
		{"vk.ban_member", "groups.ban", Arguments{UserID: 2, Reason: "spam"}}, {"vk.unban_member", "groups.unban", Arguments{UserID: 2, Reason: "reviewed"}},
		{"vk.get_content_stats", "stats.getPostReach", Arguments{PostID: 3}},
	}
	for _, tc := range fixtures {
		t.Run(tc.tool, func(t *testing.T) {
			e, v, _ := fixture(t)
			a, err := e.submit(Intent{Tool: tc.tool, Arguments: tc.args}, tc.tool, false)
			if err != nil {
				t.Fatal(err)
			}
			if a.Status == "PENDING" {
				if err = e.approve(a.ID, 1, true); err != nil {
					t.Fatal(err)
				}
			}
			if err = e.execute(context.Background(), a.ID); err != nil {
				t.Fatal(err)
			}
			if v.calls[len(v.calls)-1] != tc.method {
				t.Fatal(v.calls)
			}
			if strings.Contains(e.s.Dump("SELECT args,result,error FROM actions"), "SUPER-SECRET") {
				t.Fatal("secret in audit")
			}
		})
	}
}
