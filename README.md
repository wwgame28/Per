# Периметр — Telegram-бот для Amvera

Готовый корневой репозиторий для запуска интерактивной Telegram-игры «Периметр» на Amvera.

## Важно

- Точка входа: `app.py`.
- Конфиг Amvera: `amvera.yaml` и запасной `amvera.yml`.
- Зависимости: `requirements.txt`.
- Сохранения игроков: SQLite по пути `DB_PATH=/data/perimetr.sqlite3`.
- Админ: `8196658213`.
- Токен бота не хранится в GitHub.

## Переменные Amvera

```env
BOT_TOKEN=твой_токен_бота
ADMIN_IDS=8196658213
DB_PATH=/data/perimetr.sqlite3
PORT=8080
PYTHONUNBUFFERED=1
```

## Что есть в боте

- 530 игровых сцен генерируются движком при запуске.
- Акт II закрыт до завершения Акта I.
- Акт III закрыт до завершения Акта II.
- Психопрофиль в начале игры.
- Журнал, кодекс, карта, заметки и теории игрока.
- Скрытые последствия выборов: игрок не видит, что именно теряет.
- Картинки-заставки создаются автоматически при первом запуске.

## Быстрый запуск локально

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env
python app.py
```
