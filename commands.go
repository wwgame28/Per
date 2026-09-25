package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const help = `👩 Анна
/anna status — состояние
/anna autonomy on|off
/anna pause | resume | emergency-stop
/anna tasks | content | log | approvals | memory | team | report
/anna publish [идея] — подготовить и опубликовать
/anna schedule RFC3339 | идея — подготовить отложенный пост
/anna do задача — выбрать VK-инструмент через локальную LLM
/anna tool {"tool":"vk.get_wall","arguments":{}}
/anna delegate mira|nora|eva|viktor|alex|max текст
/anna knowledge сведения — факты о компании
/anna goals цели
/anna rules правила
/anna handoff USER_ID | takeback USER_ID
/anna blacklist USER_ID | unblacklist USER_ID
/anna spam точная фраза (от 8 символов)
/anna no-publish-today
/approve ACTION-ID
/reject ACTION-ID`

func (e *Engine) command(x Event) {
	if x.UserID != e.cfg.BossID || x.PeerID != e.cfg.BossID {
		return
	}
	text := strings.TrimSpace(x.Text)
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "/anna publish") || strings.HasPrefix(lower, "/anna do ") || strings.HasPrefix(lower, "/anna delegate ") || strings.HasPrefix(lower, "/anna tool ") {
		e.gate.Lock()
		if e.activeAutomatic && e.cancel != nil {
			e.cancel()
		}
		e.gate.Unlock()
	}
	out := ""
	var err error
	if strings.Contains(lower, "не публикуй сегодня") || strings.Contains(lower, "не публикуй сегодня ничего") || lower == "/anna no-publish-today" {
		err = e.prohibitPublication()
		out = "Публикации на сегодня остановлены. Уже отправленный в VK запрос отозвать невозможно."
	} else if strings.HasPrefix(lower, "/approve ") || strings.HasPrefix(lower, "/reject ") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			out = "Укажи ровно один Action ID."
		} else {
			err = e.approve(parts[1], x.UserID, parts[0] == "/approve")
			out = parts[1] + ": решение сохранено."
		}
	} else {
		switch {
		case lower == "/anna" || lower == "/start":
			out = help
		case lower == "/anna status":
			out = e.status()
		case lower == "/anna emergency-stop" || lower == "/anna pause":
			err = e.halt(false)
			out = "Очередь остановлена, состояние сохранено. Для продолжения /anna resume. Запросы, уже принятые VK, отозвать нельзя."
		case lower == "/anna resume":
			e.gate.Lock()
			err = e.s.Set("paused", "false")
			e.gate.Unlock()
			out = "Очередь возобновлена. Автономность: " + e.s.Get("autonomy")
		case lower == "/anna autonomy off":
			err = e.halt(true)
			out = "Автономность выключена. Команды Данила доступны."
		case lower == "/anna autonomy on":
			err = e.s.Set("autonomy", "true")
			out = "Автономность включена. Пауза: " + e.s.Get("paused")
		case lower == "/anna tasks":
			out = e.s.Dump("SELECT id,kind,status,error FROM jobs ORDER BY id DESC LIMIT 12")
		case lower == "/anna content":
			out = e.s.Dump("SELECT id,status,responsible,idea,planned,result FROM content ORDER BY id DESC LIMIT 8")
		case lower == "/anna log":
			out = e.s.Dump("SELECT id,tool,risk,status,approved_by,error FROM actions ORDER BY created DESC,rowid DESC LIMIT 12")
		case lower == "/anna approvals":
			out = e.s.Dump("SELECT id,summary,expires FROM actions WHERE status='PENDING' AND expires>? ORDER BY created LIMIT 8", time.Now().Unix())
		case lower == "/anna memory":
			e.refreshMemory()
			out = e.s.Dump("SELECT key,value FROM memory WHERE agent='anna' LIMIT 12")
		case lower == "/anna team":
			out = "Данил → Анна → Виктор → Мира, Ева, Алекс, Макс, Нора\n" + e.s.Dump("SELECT agent,text FROM dialogue ORDER BY id DESC LIMIT 7")
		case lower == "/anna report":
			out = e.report()
		case strings.HasPrefix(lower, "/anna knowledge "):
			err = e.s.Remember("anna", "knowledge", strings.TrimSpace(text[len("/anna knowledge "):]))
			out = "Подтверждённые сведения сохранены."
		case strings.HasPrefix(lower, "/anna goals "):
			err = e.s.Remember("anna", "community_goals", strings.TrimSpace(text[len("/anna goals "):]))
			out = "Цели сохранены."
		case strings.HasPrefix(lower, "/anna rules "):
			err = e.s.Remember("anna", "boss_preferences", strings.TrimSpace(text[len("/anna rules "):]))
			out = "Правила Данила обновлены. Пауза, запрет публикаций и передача диалога задаются отдельными командами."
		case strings.HasPrefix(lower, "/anna spam "):
			phrase := strings.TrimSpace(text[len("/anna spam "):])
			if len([]rune(phrase)) < 8 {
				out = "Для точного антиспама нужно минимум 8 символов."
			} else {
				err = e.s.Set("spam_phrases", clip(e.s.Get("spam_phrases")+"\n"+phrase, 3000))
				out = "Точная фраза добавлена в антиспам."
			}
		case strings.HasPrefix(lower, "/anna handoff ") || strings.HasPrefix(lower, "/anna takeback ") || strings.HasPrefix(lower, "/anna blacklist ") || strings.HasPrefix(lower, "/anna unblacklist "):
			p := strings.Fields(text)
			if len(p) != 3 {
				out = "Нужен числовой VK ID."
				break
			}
			id, parse := strconv.ParseInt(p[2], 10, 64)
			if parse != nil || id <= 0 || id == e.cfg.BossID {
				out = "Неверный VK ID."
				break
			}
			e.gate.Lock()
			if e.cancel != nil {
				e.cancel()
			}
			e.s.Exec("INSERT OR IGNORE INTO users(id) VALUES(?)", id)
			field := "handoff"
			value := 1
			if p[1] == "blacklist" || p[1] == "unblacklist" {
				field = "blacklisted"
			}
			if p[1] == "takeback" || p[1] == "unblacklist" {
				value = 0
			}
			_, err = e.s.Exec("UPDATE users SET "+field+"=? WHERE id=?", value, id)
			e.gate.Unlock()
			out = "Режим общения с " + number(id) + " обновлён."
		case lower == "/anna publish" || strings.HasPrefix(lower, "/anna publish "):
			idea := strings.TrimSpace(text[len("/anna publish"):])
			if idea == "" {
				idea = e.s.Memory("anna", "community_goals")
			}
			if idea == "" {
				out = "Укажи идею: /anna publish текст идеи"
				break
			}
			var id int64
			id, err = e.newContent(idea, false, 0)
			out = fmt.Sprint("Контент #", id, " в очереди команды.")
		case strings.HasPrefix(lower, "/anna schedule "):
			p := strings.SplitN(text[len("/anna schedule "):], "|", 2)
			if len(p) != 2 {
				out = "Формат: /anna schedule 2026-09-27T10:00:00+09:00 | идея"
				break
			}
			t, parse := time.Parse(time.RFC3339, strings.TrimSpace(p[0]))
			if parse != nil || !t.After(time.Now()) {
				out = "Нужна будущая дата RFC3339 с часовым поясом."
				break
			}
			var id int64
			id, err = e.newContent(strings.TrimSpace(p[1]), false, t.Unix())
			out = fmt.Sprint("Контент #", id, " запланирован. Публикуется только при работающем backend.")
		case strings.HasPrefix(lower, "/anna do "):
			err = e.s.Enqueue("intent", map[string]string{"text": text[len("/anna do "):]}, "intent-event:"+x.ID, false, time.Now().Unix())
			out = "Задача Анне в очереди."
		case strings.HasPrefix(lower, "/anna tool "):
			var i Intent
			err = strictJSON(text[len("/anna tool "):], &i)
			if err == nil {
				var a Action
				a, err = e.submit(i, "tool-event:"+x.ID, false)
				out = a.ID + " " + a.Status
			}
		case strings.HasPrefix(lower, "/anna delegate "):
			p := strings.SplitN(strings.TrimSpace(text[len("/anna delegate "):]), " ", 2)
			if len(p) != 2 {
				out = "Укажи агента и задачу."
				break
			}
			if _, ok := team[p[0]]; !ok {
				out = "Неизвестный агент."
				break
			}
			err = e.s.Enqueue("delegate", map[string]string{"Agent": p[0], "Text": p[1]}, "delegate:"+x.ID, false, time.Now().Unix())
			out = "Задача передана агенту."
		default:
			if strings.HasPrefix(text, "/") || strings.Contains(lower, "этому человеку") {
				out = "Для управления: /anna. Передать диалог себе: /anna handoff числовой_ID."
			} else {
				err = e.s.Enqueue("delegate", map[string]string{"Agent": "anna", "Text": "Данил пишет лично тебе: " + text}, "chat:"+x.ID, false, time.Now().Unix())
				out = "Сообщение передано Анне."
			}
		}
	}
	if err != nil {
		out = "Не выполнено: " + e.redact.Clean(err.Error())
	}
	e.notify(out)
}
func (e *Engine) status() string {
	paused := e.s.Get("paused") == "true"
	state := "работает"
	if paused {
		state = "пауза"
	}
	return fmt.Sprintf("👩 Анна\nСтатус: %s\nАвтономность: %s\nVK: %s\nАктивных задач: %d\nОжидают публикации: %d\nРешения Данила: %d\nПоследнее действие:\n%s\nСледующая задача:\n%s", state, e.s.Get("autonomy"), e.s.Get("poll_status"), e.s.Count("SELECT COUNT(*) FROM jobs WHERE status IN ('QUEUED','RUNNING')"), e.s.Count("SELECT COUNT(*) FROM content WHERE status IN ('READY','SCHEDULED')"), e.s.Count("SELECT COUNT(*) FROM actions WHERE status='PENDING' AND expires>?", time.Now().Unix()), e.s.Dump("SELECT tool,status FROM actions ORDER BY created DESC,rowid DESC LIMIT 1"), e.s.Dump("SELECT id,kind FROM jobs WHERE status='QUEUED' ORDER BY automatic,id LIMIT 1"))
}
func (e *Engine) report() string {
	day := time.Now().In(e.cfg.Location).Format("2006-01-02")
	counts := map[string]int{}
	for _, tool := range []string{"vk.create_post", "vk.reply_comment", "vk.send_message", "vk.delete_comment"} {
		counts[tool] = e.s.Count("SELECT COUNT(*) FROM actions WHERE day=? AND tool=? AND status='SUCCEEDED' AND agent='anna'", day, tool)
	}
	return fmt.Sprintf("👩 АННА — ОТЧЁТ (%s)\nОпубликовано: %d\nОтветов на комментарии: %d\nСообщений: %d\nУдалений комментариев: %d\nЛучший пост: не определён без сопоставимой статистики охвата.\nНезавершённые задачи: %d\nОжидают решения: %d\nНеопределённые результаты: %d", day, counts["vk.create_post"], counts["vk.reply_comment"], counts["vk.send_message"], counts["vk.delete_comment"], e.s.Count("SELECT COUNT(*) FROM jobs WHERE status IN ('QUEUED','RUNNING')"), e.s.Count("SELECT COUNT(*) FROM actions WHERE status='PENDING' AND expires>?", time.Now().Unix()), e.s.Count("SELECT COUNT(*) FROM actions WHERE status='UNKNOWN'"))
}
