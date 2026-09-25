# VK Tool Layer

Методы сверены с [официальной схемой VKCOM](https://github.com/VKCOM/vk-api-schema) на коммите `333481bd082ad747d4873ef4a77f9247097eeef0`. Запросы используют API `5.199`. Доступность для конкретного токена определяется реальным ответом VK; наличие метода в схеме не означает наличие прав у аккаунта.

| Инструмент | Реальный метод | Выбранный токен | Риск |
|---|---|---|---|
| `vk.get_wall` | `wall.get` | пользователь | LOW |
| `vk.create_post` | `wall.post` | пользователь | MEDIUM, сверх дневного лимита HIGH |
| `vk.edit_post` | `wall.edit` | пользователь | HIGH — консервативная защита охвата |
| `vk.delete_post` | `wall.delete` | пользователь | HIGH |
| `vk.get_comments` | `wall.getComments` | пользователь | LOW |
| `vk.reply_comment` | `wall.createComment` | сообщество | MEDIUM |
| `vk.delete_comment` | `wall.deleteComment` | пользователь | MEDIUM только при точном backend-совпадении с blacklist фраз; иначе HIGH |
| `vk.get_messages` | `messages.getHistory` | сообщество | LOW; только известный входящий диалог/Данил |
| `vk.send_message` | `messages.send` | сообщество | LOW; только известный входящий диалог/Данил |
| `vk.upload_photo` | `photos.getWallUploadServer` → HTTPS upload → `photos.saveWallPhoto` | пользователь | MEDIUM |
| `vk.get_members` | `groups.getMembers` | сообщество | LOW |
| `vk.get_statistics` | `stats.get` | пользователь | LOW |
| `vk.get_community_info` | `groups.getById` | сообщество | LOW |
| `vk.ban_member` | проверка `groups.getMembers(filter=managers)` → `groups.ban` | пользователь для бана | HIGH; администраторы/владелец запрещены |
| `vk.unban_member` | проверка managers → `groups.unban` | пользователь | HIGH |
| `vk.get_content_stats` | `stats.getPostReach` | пользователь | LOW; VK может ограничивать статистику |
| `vk.get_recent_activity` | локальный агрегат SQLite | не нужен | LOW; не выдуманный метод VK |

Токен выбирает backend, LLM не задаёт его, версию API, домен, `owner_id`, `group_id` или произвольный метод. Все действия привязаны к одному `VK_GROUP_ID`. Публичные сообщения идут в отдельный контур ответов и не запускают произвольные намерения.

Ошибки API: сохраняется только числовой код, без `request_params`, токена, upload URL и сырых ошибок HTTP. При ошибке прав/типа токена Анна уведомляет Данила. Транспортная ошибка записи — `UNKNOWN`, без автоматического повтора. `guid` и `random_id` стабильны для конкретного Action ID; они не заменяют осторожное восстановление после сетевой неопределённости.

Не поддерживаются: назначение администраторов, передача прав, управление секретами, смена критических настроек, массовое удаление. Такие инструменты отвергаются на валидации и не могут быть разрешены кнопкой `/approve`.

Ссылки конфигурации:

- [llama-server](https://github.com/ggml-org/llama.cpp/tree/b6650/tools/server)
- [Qwen3-0.6B](https://huggingface.co/Qwen/Qwen3-0.6B)
- [Используемая GGUF-квантизация](https://huggingface.co/unsloth/Qwen3-0.6B-GGUF)
- [Docker в Amvera](https://docs.amvera.ru/applications/configuration/docker.html)
- [Постоянные данные Amvera](https://docs.amvera.ru/general/FAQ/data-saving.html)
