package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// Optional integration: real local llama-server, fake VK (no public side effects).
func TestLiveLocalPipeline(t *testing.T) {
	if os.Getenv("SHTAB_LIVE_LLM") != "1" {
		t.Skip("set SHTAB_LIVE_LLM=1 with llama-server on loopback")
	}
	e, _, _ := fixture(t)
	e.llm = &Llama{URL: "http://127.0.0.1:8081", Client: &http.Client{Timeout: 5 * time.Minute}, redact: e.redact}
	e.s.Remember("anna", "knowledge", "Сообщество публикует короткие художественные наблюдения. Не продаёт товары и услуги.")
	e.s.Remember("anna", "community_goals", "Короткие спокойные посты, без цифр и рекламных обещаний.")
	id, err := e.newContent("Короткое художественное наблюдение о тихом утре, максимум два предложения. Без цен и фактов о компании.", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = e.content(context.Background(), id, true); err != nil {
		t.Log(e.s.Dump("SELECT agent,text FROM dialogue ORDER BY id"))
		t.Fatal(err)
	}
	var aid string
	e.s.QueryRow("SELECT action_id FROM content WHERE id=?", id).Scan(&aid)
	if aid == "" {
		t.Fatal("no action")
	}
	if err = e.execute(context.Background(), aid); err != nil {
		t.Fatal(err)
	}
	t.Logf("real inference pipeline completed in %s; VK transport mocked", time.Since(started).Round(time.Millisecond))
}
