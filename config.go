package main

import (
	"errors"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GroupID, BossID                                    int64
	GroupToken, UserToken, DBPath, LlamaURL, AssetsDir string
	Autonomy                                           bool
	MaxDaily, MaxPosts, Cooldown                       int
	Initiative                                         time.Duration
	Location                                           *time.Location
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func loadConfig() (Config, error) {
	c := Config{GroupToken: os.Getenv("VK_TOKEN"), UserToken: os.Getenv("VK_USER_TOKEN"), DBPath: env("DB_PATH", "/data/shtab.sqlite3"), LlamaURL: env("LLAMA_URL", "http://127.0.0.1:8081"), AssetsDir: env("ASSETS_DIR", "/data/assets"), Autonomy: env("ANNA_AUTONOMY", "false") == "true"}
	c.GroupID, _ = strconv.ParseInt(os.Getenv("VK_GROUP_ID"), 10, 64)
	c.BossID, _ = strconv.ParseInt(os.Getenv("DANIL_VK_ID"), 10, 64)
	c.MaxDaily, _ = strconv.Atoi(env("MAX_AUTONOMOUS_MESSAGES", "30"))
	c.MaxPosts, _ = strconv.Atoi(env("MAX_POSTS_PER_DAY", "2"))
	c.Cooldown, _ = strconv.Atoi(env("USER_COOLDOWN_SECONDS", "120"))
	mins, _ := strconv.Atoi(env("INITIATIVE_INTERVAL_MINUTES", "0"))
	c.Initiative = time.Duration(mins) * time.Minute
	var err error
	c.Location, err = time.LoadLocation(env("TZ", "Asia/Yakutsk"))
	if err != nil {
		return c, errors.New("invalid TZ")
	}
	u, e := url.Parse(c.LlamaURL)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return c, errors.New("LLAMA_URL must be loopback HTTP without credentials")
	}
	if c.GroupID <= 0 || c.BossID <= 0 || c.GroupToken == "" {
		return c, errors.New("VK_GROUP_ID, DANIL_VK_ID and VK_TOKEN are required")
	}
	if c.MaxDaily < 1 || c.MaxDaily > 200 || c.MaxPosts < 1 || c.MaxPosts > 10 || c.Cooldown < 10 || mins < 0 || (mins > 0 && mins < 60) {
		return c, errors.New("invalid action limits")
	}
	return c, nil
}

type Redactor struct{ secrets []string }

var secretPattern = regexp.MustCompile(`(?i)(access_token|vk_token|vk_user_token|password|authorization|api_key)\s*[=:]\s*[^\s&,;]+`)

func (r Redactor) Clean(s string) string {
	for _, v := range r.secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return secretPattern.ReplaceAllString(s, "[REDACTED]")
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func number(n int64) string { return strconv.FormatInt(n, 10) }
