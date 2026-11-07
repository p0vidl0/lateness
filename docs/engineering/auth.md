# Вход и приглашения

## Регистрация закрыта

Новый аккаунт создаётся только по одноразовому приглашению. Вместе с аккаунтом появляется первый проект «Мой проект».

- Приглашение живёт 7 дней. В базе хранится sha256 токена, сам токен виден один раз — в ссылке.
- Выдаёт приглашения администратор на странице «Приглашения» (`/admin/invites`) или CLI `lateness invite [-admin] [-note …]` (в dev — `make invite`). Отзыв проставляет `revoked_at`.
- Флаг «Администратор» в приглашении делает новый аккаунт администратором. Администратор только выдаёт приглашения; доступа к чужим проектам у него нет.
- Использование приглашения — одна транзакция: `SELECT … FOR UPDATE` строки приглашения, создание аккаунта, identity и проекта, отметка `used_at`. Две параллельные регистрации по одной ссылке не пройдут обе.

## Пароль

Почта и пароль от 8 символов, bcrypt. Сессия — cookie `lateness_session` (HttpOnly, SameSite=Lax, Secure при `https`), в базе — sha256 токена.

## Провайдеры (VK ID, Telegram)

Провайдер реализует `app.AuthProvider`: `AuthURL` и `Exchange` по схеме authorization code + PKCE (S256).

| Провайдер | Код | Особенности |
|---|---|---|
| VK ID | `adapters/authprovider/vk.go` | Токен и профиль — через `id.vk.com/oauth2/auth` и `oauth2/user_info`, в callback приходит `device_id`. Email отдаётся, если пользователь разрешил. |
| Telegram | `adapters/authprovider/oidc.go`, `NewTelegram` | OpenID Connect на `oauth.telegram.org`: `scope=openid profile`, токен-эндпоинт с Basic-авторизацией, `id_token` проверяется по JWKS, issuer и client id. Идентификатор — claim `id` (Telegram user id). Email Telegram не отдаёт. |

`OIDC` — общий тип: другой OIDC-провайдер — это ещё один экземпляр с адресами, issuer и claim идентификатора. Email из `id_token` учитывается только при `email_verified = true`, иначе по нему нельзя было бы захватить чужой аккаунт.

1. `POST /auth/{provider}` (форма без JS) кладёт state, PKCE-verifier, токен приглашения и режим (вход или привязка) в cookie `lateness_oauth` (HttpOnly, SameSite=Lax, Path=/auth/, 10 минут) и уводит на провайдера.
2. `GET /auth/{provider}/callback` сверяет `state` с cookie, удаляет cookie и вызывает `Exchange`. Дальше — `app.LoginExternal` или, в режиме привязки, `app.LinkIdentity`.
3. `LoginExternal`:
   - identity `(provider, subject)` уже есть — вход;
   - провайдер вернул email, совпадающий с аккаунтом, — identity привязывается к нему;
   - иначе нужен действующий инвайт из cookie — создаётся аккаунт (email может быть пустым);
   - инвайта нет — «Вход только по приглашению».

Ошибки возвращают на `/login?error=…`, на страницу приглашения или в профиль.

## Профиль

`/profile` — имя, способы входа, пароль.

- **Привязка.** Кнопка «Привязать» отправляет `POST /auth/{provider}` с `link=1`; без сессии — редирект на вход. После callback `LinkIdentity` прикрепляет identity к текущему аккаунту. Identity другого аккаунта и второй аккаунт того же провайдера — отказ (`ErrConflict`, «уже привязан к другому пользователю»). На уровне схемы: первичный ключ `(provider, subject)` и уникальный `(account_id, provider)`.
- **Отвязка.** `POST /profile/identities/{provider}/unlink`. Последний способ входа отвязать нельзя: должен остаться пароль или другой провайдер. Строка `identities` удаляется по-настоящему: это связка для входа, а не данные пользователя, и её можно привязать снова, в том числе к другому аккаунту.
- **Пароль.** Смена требует текущего пароля. Аккаунт, созданный через провайдера, задаёт здесь почту (она станет логином) и пароль. После смены пароля все остальные сессии завершаются.
- Провайдер, который больше не настроен на сервере, остаётся в списке привязанных: его можно отвязать, но не привязать.

### Настройка VK ID

1. Создать приложение в кабинете VK ID, тип — веб.
2. Доверенный redirect URI: `${LATENESS_PUBLIC_URL}/auth/vk/callback`.
3. Задать `LATENESS_VK_CLIENT_ID` и `LATENESS_VK_CLIENT_SECRET` (в dev — в `.docker/env/lateness-dev.local.env`).

### Настройка Telegram

1. В @BotFather у бота включить Web Login (OpenID Connect) и указать домен сайта и redirect URI `${LATENESS_PUBLIC_URL}/auth/telegram/callback`.
2. Задать `LATENESS_TELEGRAM_LOGIN_CLIENT_ID` и `LATENESS_TELEGRAM_LOGIN_CLIENT_SECRET` из настроек бота.

Это не связано с Telegram-outlet: там токен бота задаётся в самом канале уведомлений.

### Новый OIDC-провайдер

Создать экземпляр `authprovider.NewOIDC` с адресами авторизации, токена, JWKS, issuer и claim идентификатора, добавить конфиг `LATENESS_<NAME>_*` и регистрацию в `cmd/lateness/serve.go`. Страницы, cookie, профиль и `LoginExternal` менять не нужно.
