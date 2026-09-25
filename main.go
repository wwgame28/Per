package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	c, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	s, err := openStore(c.DBPath)
	if err != nil {
		log.Fatal("database initialization failed")
	}
	defer s.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	v := newVK(c)
	l := &Llama{URL: c.LlamaURL, Client: &http.Client{Timeout: 5 * time.Minute}, redact: Redactor{[]string{c.GroupToken, c.UserToken}}}
	e := newEngine(c, s, v, l)
	var wg sync.WaitGroup
	for _, run := range []func(context.Context){e.runNotifications, e.run, e.poll} {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(ctx) }(run)
	}
	e.dispatch()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.PingContext(r.Context()) != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.Write([]byte("shtab alive\n"))
	})
	srv := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Print("health server failed")
			stop()
		}
	}()
	log.Print("SHTAB started; no tokens logged")
	<-ctx.Done()
	e.gate.Lock()
	if e.cancel != nil {
		e.cancel()
	}
	e.gate.Unlock()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
	wg.Wait()
}
