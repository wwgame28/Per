# Запуск на Amvera

1. Подключи репозиторий `wwgame28/Per`, ветка `main`, root directory `/`.
2. Убедись, что в корне видны `app.py`, `requirements.txt`, `amvera.yaml`, `Dockerfile`.
3. Добавь переменные окружения:

```env
BOT_TOKEN=твой_токен_бота
ADMIN_IDS=8196658213
DB_PATH=/data/perimetr.sqlite3
PORT=8080
PYTHONUNBUFFERED=1
```

`BOT_TOKEN` не добавляй в GitHub. Его нужно хранить только в переменных/секретах Amvera.

Если Amvera пишет, что не найден `app.py` или `requirements.txt`, значит подключён не этот репозиторий или выбран не корень проекта.
