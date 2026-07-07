import asyncio
import html
import json
import os
import sqlite3
import threading
from datetime import datetime
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from typing import Any, Dict

try:
    from dotenv import load_dotenv
    load_dotenv()
except Exception:
    pass

from aiogram import Bot, Dispatcher, F
from aiogram.client.default import DefaultBotProperties
from aiogram.enums import ParseMode
from aiogram.filters import Command, CommandStart
from aiogram.types import CallbackQuery, FSInputFile, InlineKeyboardButton, InlineKeyboardMarkup, Message

BASE = Path(__file__).resolve().parent
TOKEN = os.getenv("BOT_TOKEN", "").strip()
ADMIN_IDS = {int(x) for x in os.getenv("ADMIN_IDS", "8196658213").replace(",", " ").split() if x.isdigit()}
DB_PATH = Path(os.getenv("DB_PATH", "/data/perimetr.sqlite3"))
PORT = int(os.getenv("PORT", "8080"))
SCENES: Dict[str, Dict[str, Any]] = {}

IMAGE_MAP = {
    "SCENE_001": "start_screen.png",
    "SCENE_016": "fedor.png",
    "SCENE_030": "gates.png",
    "SCENE_045": "artem.png",
    "SCENE_048": "marina.png",
    "SCENE_052": "gleb.png",
    "SCENE_056": "eva.png",
    "SCENE_090": "generator.png",
    "SCENE_120": "sergey.png",
    "SCENE_201": "empty_seat.png",
    "SCENE_246": "trial.png",
    "SCENE_320": "radio.png",
    "SCENE_361": "second_night.png",
    "SCENE_461": "archive.png",
    "SCENE_501": "zorin.png",
    "SCENE_541": "three_truths.png",
    "SCENE_575": "final_choice.png",
    "SCENE_621": "ng_plus.png",
}

SPECIAL = {
    "SCENE_001": ("Последняя спокойная дорога", "Автобус подпрыгивает на бетонных плитах. За окнами мокрый лес. Артём шутит, Марина молчит, Ева листает папку, а Глеб слишком спокойно смотрит на карту."),
    "SCENE_016": ("Фёдор у шлагбаума", "Сторож открывает проход и говорит почти без голоса: «Если услышите свой голос — не отвечайте». После этой фразы даже дождь кажется тише."),
    "SCENE_030": ("Ворота закрываются", "Металлические створки сходятся за спинами. Щелчок замка звучит так, будто комплекс сделал первый выбор за вас."),
    "SCENE_045": ("Фотография Сергея", "Артём показывает фото брата. На снимке Сергей стоит у двери, которую никто ещё не видел внутри объекта."),
    "SCENE_048": ("Разметка Марины", "Марина находит инженерную метку с фамилией подрядчика. Она произносит имя слишком тихо, но вы понимаете: это связано с её отцом."),
    "SCENE_052": ("Старое имя коридора", "Глеб называет закрытый сектор старым служебным названием. На карте такого названия нет."),
    "SCENE_056": ("Анкета Евы", "Ева прячет медицинскую анкету с пометкой PX-17. Потом просит: «Если я начну оправдывать фонд — останови меня»."),
    "SCENE_090": ("Генератор и первое решение", "На щите четыре линии: свет, связь, медблок, архив. Энергии хватит только на одно направление."),
    "SCENE_120": ("Серёга?", "Рация включается сама. Артём слышит голос брата и впервые перестаёт шутить."),
    "SCENE_201": ("Пустое место за столом", "Утром за столом одно место пустое. Кружка остыла, а на краю лежит предмет, которого ночью не было."),
    "SCENE_246": ("Суд за столом", "Команда пытается восстановить ночь. В каждой версии есть человек, который врёт — или помнит не то."),
    "SCENE_320": ("Рация говорит вашим голосом", "Из помех звучит ваш собственный голос. Он повторяет фразу из первого дня, но меняет одно слово."),
    "SCENE_361": ("Не отвечайте, если услышите меня", "Марина сидит рядом и молчит. Но её голос зовёт из коридора."),
    "SCENE_461": ("Центральный архив", "Здесь нет монстра. Только документы, которых слишком много, чтобы оправдаться незнанием."),
    "SCENE_501": ("Зорин за стеклом", "Зорин выглядит не злодеем, а человеком, который слишком долго объяснял себе компромиссы."),
    "SCENE_541": ("Три правды", "Перед вами имена погибших, формула PX-17 и путь наружу. Все три нельзя унести вместе."),
    "SCENE_575": ("Выбор принят", "Комплекс гудит так низко, что вибрация проходит через кости. Вы уже знаете достаточно."),
    "SCENE_621": ("Фёдор закрывает журнал", "В журнале у ворот уже есть ваша фамилия. Чернила свежие."),
}


def scene_id(number: int) -> str:
    return f"SCENE_{number:03d}"


def act_of(scene: str) -> int:
    n = int(scene.split("_")[1])
    if n <= 120:
        return 1
    if n <= 380:
        return 2
    return 3


def next_scene(n: int, step: int = 1) -> str:
    if n <= 120:
        return scene_id(min(120, n + step))
    if n <= 380:
        return scene_id(min(380, n + step))
    return scene_id(min(630, n + step))


def choice(label: str, next_id: str | None, effects: Dict[str, Any] | None = None) -> Dict[str, Any]:
    return {"label": label, "next": next_id, "effects": effects or {}}


def build_scenes() -> Dict[str, Dict[str, Any]]:
    scenes: Dict[str, Dict[str, Any]] = {}
    ids = list(range(1, 121)) + list(range(201, 381)) + list(range(401, 631))
    short_titles = ["Тихий коридор", "Пауза в рации", "След на пыли", "Дверь без номера", "Сломанная камера", "Лестница вниз"]
    doc_titles = ["Обрывок отчёта", "Журнал Фёдора", "Протокол PX-17", "Приказ фонда", "Список недобровольцев"]
    for index, number in enumerate(ids):
        sid = scene_id(number)
        title, text = SPECIAL.get(sid, (doc_titles[index % len(doc_titles)] if index % 5 == 0 else short_titles[index % len(short_titles)], ""))
        if not text:
            if index % 5 == 0:
                text = f"Вы находите документ: «{title}». Он не объясняет всё, но меняет порядок вопросов."
            else:
                text = f"{title}. Деталь цепляет взгляд: открытая защёлка, чужой след, пауза в шуме вентиляции. Комплекс будто ждёт, какой смысл вы сами этому дадите."
        choices = [
            choice("Проверить деталь", next_scene(number, 1), {"knowledge": 1}),
            choice("Пойти на звук", next_scene(number, 2), {"fear": 1, "px17": 1}),
            choice("Поговорить с тем, кто рядом", next_scene(number, 1), {"trust_team": 1}),
            choice("Промолчать и наблюдать", next_scene(number, 1), {"inner_voice": 1}),
        ]
        if index % 5 == 0:
            choices = [
                choice("Забрать документ", next_scene(number, 1), {"knowledge": 1, "doc": sid}),
                choice("Сфотографировать страницу", next_scene(number, 2), {"evidence": 1, "doc": sid}),
                choice("Показать находку команде", next_scene(number, 1), {"trust_team": 1, "doc": sid}),
            ]
        if sid == "SCENE_090":
            choices = [
                choice("Подать питание на свет", next_scene(number, 1), {"power_light": 1}),
                choice("Подать питание на связь", next_scene(number, 2), {"power_radio": 1}),
                choice("Подать питание на медблок", next_scene(number, 3), {"power_med": 1}),
                choice("Подать питание на архив", next_scene(number, 4), {"power_archive": 1, "knowledge": 2}),
            ]
        if sid == "SCENE_541":
            choices = [
                choice("Вынести имена погибших", next_scene(number, 4), {"people": 1}),
                choice("Сохранить доказательства", next_scene(number, 2), {"truth": 1, "evidence": 2}),
                choice("Уничтожить формулу", next_scene(number, 3), {"formula_destroyed": 1}),
            ]
        if sid == "SCENE_120":
            choices = [choice("Остаться с Артёмом", None, {"act1_done": 1, "unlock": 2})]
        if sid == "SCENE_380":
            choices = [choice("Войти в третий день", None, {"act2_done": 1, "unlock": 3})]
        if sid == "SCENE_630":
            choices = [choice("Вернуться в меню", None, {"finished": 1})]
        scenes[sid] = {"id": sid, "act": act_of(sid), "title": title, "text": text, "image": IMAGE_MAP.get(sid), "choices": choices}
    return scenes


def init_dirs() -> None:
    (BASE / "assets" / "images").mkdir(parents=True, exist_ok=True)
    DB_PATH.parent.mkdir(parents=True, exist_ok=True)


def make_images() -> None:
    try:
        from PIL import Image, ImageDraw, ImageFont
    except Exception:
        return
    labels = {name: scene.replace("SCENE_", "СЦЕНА ") for scene, name in IMAGE_MAP.items()}
    labels["start_screen.png"] = "НИИ ПЕРИМЕТР"
    for filename, title in labels.items():
        path = BASE / "assets" / "images" / filename
        if path.exists():
            continue
        image = Image.new("RGB", (1280, 720), (14, 20, 26))
        draw = ImageDraw.Draw(image)
        font = ImageFont.load_default()
        for y in range(720):
            draw.line([(0, y), (1280, y)], fill=(14 + y // 20, 20 + y // 24, 26 + y // 28))
        draw.rectangle([80, 90, 1200, 630], outline=(135, 145, 140), width=2)
        draw.rectangle([0, 610, 1280, 720], fill=(7, 8, 10))
        draw.ellipse([1040, 80, 1130, 170], fill=(136, 130, 105))
        draw.text((96, 520), title, font=font, fill=(235, 232, 215))
        draw.text((96, 560), "если услышите свой голос — не отвечайте", font=font, fill=(170, 176, 170))
        image.save(path, "PNG", optimize=True)


def init_db() -> None:
    with sqlite3.connect(DB_PATH) as con:
        con.execute("CREATE TABLE IF NOT EXISTS saves(user_id INTEGER PRIMARY KEY, state TEXT NOT NULL, updated_at TEXT NOT NULL)")


def default_state() -> Dict[str, Any]:
    return {"scene": None, "unlock": 1, "vars": {}, "docs": [], "history": [], "notes": [], "images": True}


def load_state(user_id: int) -> Dict[str, Any]:
    with sqlite3.connect(DB_PATH) as con:
        row = con.execute("SELECT state FROM saves WHERE user_id=?", (user_id,)).fetchone()
    return json.loads(row[0]) if row else default_state()


def save_state(user_id: int, state: Dict[str, Any]) -> None:
    with sqlite3.connect(DB_PATH) as con:
        con.execute("INSERT OR REPLACE INTO saves VALUES(?,?,?)", (user_id, json.dumps(state, ensure_ascii=False), datetime.utcnow().isoformat()))


def apply_effects(state: Dict[str, Any], effects: Dict[str, Any]) -> None:
    for key, value in effects.items():
        if key == "doc":
            if value not in state["docs"]:
                state["docs"].append(value)
        elif key == "unlock":
            state["unlock"] = max(state.get("unlock", 1), int(value))
        else:
            state["vars"][key] = state["vars"].get(key, 0) + value if isinstance(value, int) else value


def menu_keyboard(state: Dict[str, Any]) -> InlineKeyboardMarkup:
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="Продолжить" if state.get("scene") else "Начать вылазку", callback_data="m:continue")],
        [InlineKeyboardButton(text="Акты", callback_data="m:acts"), InlineKeyboardButton(text="Брифинг", callback_data="m:brief")],
        [InlineKeyboardButton(text="Журнал", callback_data="m:journal"), InlineKeyboardButton(text="Кодекс", callback_data="m:codex")],
        [InlineKeyboardButton(text="Карта", callback_data="m:map"), InlineKeyboardButton(text="Картинки on/off", callback_data="m:images")],
    ])


def acts_keyboard(state: Dict[str, Any]) -> InlineKeyboardMarkup:
    unlocked = state.get("unlock", 1)
    return InlineKeyboardMarkup(inline_keyboard=[
        [InlineKeyboardButton(text="Акт I: Вход", callback_data="a:1")],
        [InlineKeyboardButton(text="Акт II: Распад" if unlocked >= 2 else "Акт II: закрыт", callback_data="a:2")],
        [InlineKeyboardButton(text="Акт III: Цена выбора" if unlocked >= 3 else "Акт III: закрыт", callback_data="a:3")],
        [InlineKeyboardButton(text="Назад", callback_data="m:home")],
    ])


def scene_keyboard(scene: Dict[str, Any]) -> InlineKeyboardMarkup:
    rows = [[InlineKeyboardButton(text=item["label"], callback_data=f"c:{scene['id']}:{index}")] for index, item in enumerate(scene["choices"])]
    rows.append([InlineKeyboardButton(text="Записать вывод", callback_data="m:note")])
    rows.append([InlineKeyboardButton(text="Меню", callback_data="m:home")])
    return InlineKeyboardMarkup(inline_keyboard=rows)


def profile_keyboard(step: int) -> InlineKeyboardMarkup:
    questions = [
        [("Сначала люди", "people"), ("Сначала факты", "truth"), ("Сначала выход", "exit")],
        [("Доверять команде", "team"), ("Проверять каждого", "control"), ("Слушать объект", "echo")],
        [("Ответить голосу", "answer"), ("Промолчать", "silent"), ("Найти источник", "source")],
    ]
    return InlineKeyboardMarkup(inline_keyboard=[[InlineKeyboardButton(text=text, callback_data=f"p:{step}:{value}")] for text, value in questions[step]])


async def send_menu(bot: Bot, chat_id: int, user_id: int) -> None:
    state = load_state(user_id)
    caption = "<b>НИИ «Периметр»</b>\nДоступ восстановлен. Выборы не показывают цену — последствия проявятся позже."
    image = BASE / "assets" / "images" / "start_screen.png"
    if state.get("images") and image.exists():
        await bot.send_photo(chat_id, FSInputFile(image), caption=caption, reply_markup=menu_keyboard(state), parse_mode=ParseMode.HTML)
    else:
        await bot.send_message(chat_id, caption, reply_markup=menu_keyboard(state), parse_mode=ParseMode.HTML)


async def show_scene(bot: Bot, chat_id: int, user_id: int, scene_id_value: str) -> None:
    state = load_state(user_id)
    scene = SCENES[scene_id_value]
    if scene["act"] > state.get("unlock", 1):
        await bot.send_message(chat_id, "Этот акт пока закрыт.", reply_markup=acts_keyboard(state))
        return
    state["scene"] = scene_id_value
    save_state(user_id, state)
    title = f"<b>{scene_id_value} — {html.escape(scene['title'])}</b>\n<i>Акт {scene['act']}</i>"
    image = BASE / "assets" / "images" / str(scene.get("image") or "")
    if state.get("images") and scene.get("image") and image.exists():
        await bot.send_photo(chat_id, FSInputFile(image), caption=title, parse_mode=ParseMode.HTML)
        await bot.send_message(chat_id, html.escape(scene["text"]), reply_markup=scene_keyboard(scene), parse_mode=ParseMode.HTML)
    else:
        await bot.send_message(chat_id, title + "\n\n" + html.escape(scene["text"]), reply_markup=scene_keyboard(scene), parse_mode=ParseMode.HTML)


async def cmd_start(message: Message, bot: Bot) -> None:
    await send_menu(bot, message.chat.id, message.from_user.id)


async def cmd_new(message: Message) -> None:
    save_state(message.from_user.id, default_state())
    await message.answer("<b>Психопрофиль 1/3</b>\nЧто важнее, если всё пойдёт не по плану?", reply_markup=profile_keyboard(0), parse_mode=ParseMode.HTML)


async def cmd_continue(message: Message, bot: Bot) -> None:
    state = load_state(message.from_user.id)
    await show_scene(bot, message.chat.id, message.from_user.id, state.get("scene") or "SCENE_001")


async def cmd_note(message: Message) -> None:
    text = message.text.partition(" ")[2].strip()
    if not text:
        await message.answer("Напиши так: /note твой вывод")
        return
    state = load_state(message.from_user.id)
    state["notes"].append({"scene": state.get("scene"), "text": text})
    save_state(message.from_user.id, state)
    await message.answer("Записано.")


async def cmd_theory(message: Message) -> None:
    text = message.text.partition(" ")[2].strip()
    if not text:
        await message.answer("Напиши так: /theory твоя версия событий")
        return
    state = load_state(message.from_user.id)
    state["notes"].append({"scene": state.get("scene"), "text": "ТЕОРИЯ: " + text})
    save_state(message.from_user.id, state)
    await message.answer("Теория сохранена.")


async def cmd_goto(message: Message, bot: Bot) -> None:
    if message.from_user.id not in ADMIN_IDS:
        return
    target = message.text.partition(" ")[2].strip().upper()
    if target in SCENES:
        state = load_state(message.from_user.id)
        state["unlock"] = max(state.get("unlock", 1), act_of(target))
        save_state(message.from_user.id, state)
        await show_scene(bot, message.chat.id, message.from_user.id, target)


async def cmd_debug(message: Message) -> None:
    if message.from_user.id in ADMIN_IDS:
        data = json.dumps(load_state(message.from_user.id), ensure_ascii=False, indent=2)[:3500]
        await message.answer("<pre>" + html.escape(data) + "</pre>", parse_mode=ParseMode.HTML)


async def on_menu(callback: CallbackQuery, bot: Bot) -> None:
    action = callback.data.split(":")[1]
    state = load_state(callback.from_user.id)
    if action == "home":
        await send_menu(bot, callback.message.chat.id, callback.from_user.id)
    elif action == "continue":
        await show_scene(bot, callback.message.chat.id, callback.from_user.id, state.get("scene") or "SCENE_001")
    elif action == "acts":
        await callback.message.answer("Выберите акт:", reply_markup=acts_keyboard(state))
    elif action == "brief":
        await callback.message.answer("Брифинг: найти пропавшую экспедицию и понять, почему объект снова подал сигнал.", reply_markup=menu_keyboard(state))
    elif action == "journal":
        await callback.message.answer("\n".join(item["text"] for item in state["notes"][-10:]) or "Журнал пуст.")
    elif action == "codex":
        await callback.message.answer("\n".join(state["docs"][-20:]) or "Документов пока нет.")
    elif action == "map":
        await callback.message.answer(f"Двор открыт. Внутренние сектора: {'открыты' if state.get('unlock', 1) >= 2 else 'закрыты'}. Архив: {'открыт' if state.get('unlock', 1) >= 3 else 'закрыт'}.")
    elif action == "images":
        state["images"] = not state.get("images", True)
        save_state(callback.from_user.id, state)
        await callback.message.answer("Настройка картинок изменена.", reply_markup=menu_keyboard(state))
    elif action == "note":
        await callback.message.answer("Напиши /note вывод или /theory версия событий")
    await callback.answer()


async def on_act(callback: CallbackQuery, bot: Bot) -> None:
    state = load_state(callback.from_user.id)
    selected = int(callback.data.split(":")[1])
    if selected > state.get("unlock", 1):
        await callback.answer("Акт закрыт", show_alert=True)
        return
    await show_scene(bot, callback.message.chat.id, callback.from_user.id, {1: "SCENE_001", 2: "SCENE_201", 3: "SCENE_401"}[selected])
    await callback.answer()


async def on_profile(callback: CallbackQuery, bot: Bot) -> None:
    state = load_state(callback.from_user.id)
    _, step_text, value = callback.data.split(":")
    step = int(step_text)
    state["vars"][value] = state["vars"].get(value, 0) + 1
    save_state(callback.from_user.id, state)
    if step < 2:
        await callback.message.answer(f"<b>Психопрофиль {step + 2}/3</b>\nВыберите реакцию.", reply_markup=profile_keyboard(step + 1), parse_mode=ParseMode.HTML)
    else:
        await callback.message.answer("Профиль сохранён. Ворота открываются.")
        await show_scene(bot, callback.message.chat.id, callback.from_user.id, "SCENE_001")
    await callback.answer()


async def on_choice(callback: CallbackQuery, bot: Bot) -> None:
    _, scene_key, index_text = callback.data.split(":")
    state = load_state(callback.from_user.id)
    if state.get("scene") != scene_key:
        await callback.answer("Этот выбор устарел", show_alert=True)
        return
    scene = SCENES[scene_key]
    selected = scene["choices"][int(index_text)]
    apply_effects(state, selected["effects"])
    state["history"].append([scene_key, selected["label"]])
    save_state(callback.from_user.id, state)
    if scene_key == "SCENE_120":
        await callback.message.answer("Акт I завершён. Акт II открыт.", reply_markup=acts_keyboard(load_state(callback.from_user.id)))
    elif scene_key == "SCENE_380":
        await callback.message.answer("Акт II завершён. Акт III открыт.", reply_markup=acts_keyboard(load_state(callback.from_user.id)))
    elif not selected["next"]:
        await send_menu(bot, callback.message.chat.id, callback.from_user.id)
    else:
        await show_scene(bot, callback.message.chat.id, callback.from_user.id, selected["next"])
    await callback.answer()


def start_health_server() -> None:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
        def log_message(self, *args):
            pass
    threading.Thread(target=lambda: HTTPServer(("0.0.0.0", PORT), Handler).serve_forever(), daemon=True).start()


async def run_bot() -> None:
    if not TOKEN:
        raise RuntimeError("BOT_TOKEN is not set")
    bot = Bot(TOKEN, default=DefaultBotProperties(parse_mode=ParseMode.HTML))
    dp = Dispatcher()
    dp.message.register(cmd_start, CommandStart())
    dp.message.register(cmd_start, Command("menu"))
    dp.message.register(cmd_new, Command("new"))
    dp.message.register(cmd_continue, Command("continue"))
    dp.message.register(cmd_note, Command("note"))
    dp.message.register(cmd_theory, Command("theory"))
    dp.message.register(cmd_goto, Command("goto"))
    dp.message.register(cmd_debug, Command("debug"))
    dp.callback_query.register(on_menu, F.data.startswith("m:"))
    dp.callback_query.register(on_act, F.data.startswith("a:"))
    dp.callback_query.register(on_profile, F.data.startswith("p:"))
    dp.callback_query.register(on_choice, F.data.startswith("c:"))
    await dp.start_polling(bot)


def main() -> None:
    global SCENES
    init_dirs()
    init_db()
    make_images()
    SCENES = build_scenes()
    start_health_server()
    asyncio.run(run_bot())


if __name__ == "__main__":
    main()
