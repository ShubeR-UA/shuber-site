# ShubeR: что нажимать в Cloudflare Dashboard

Этот файл нужен именно для публикации через Dashboard.

## 1. R2

Открой:

**Cloudflare Dashboard -> R2 Object Storage -> Create bucket**

Название:

```text
shuber-media
```

После создания bucket оставь его приватным. Публичная раздача не нужна: `/media/*` отдаёт Worker через R2 binding.

## 2. GitHub

Залей распакованный проект в новый GitHub repository. В корне должны лежать:

```text
wrangler.jsonc
Dockerfile.cloudflare
package.json
src/
internal/
web/
```

## 3. Worker

В Cloudflare:

**Workers & Pages -> Create application -> Import a repository**

Выбери GitHub repository.

Для production deploy command укажи:

```text
npx wrangler deploy
```

Для проекта с Containers важно именно `wrangler deploy`, а не `wrangler versions upload`, потому что только `wrangler deploy` публикует контейнерный image и делает rollout контейнеров.

## 4. Secret

Открой Worker:

**Settings -> Variables and Secrets -> Add -> Secret**

Создай:

```text
SHUBER_ADMIN_PASSWORD
```

Значение: придумай отдельный сильный пароль.

## 5. Domain

После первого успешного deploy:

**Settings -> Domains & Routes -> Add -> Custom Domain**

Например:

```text
music.example.com
```

## 6. Проверка

Открой сайт.

Войди как `admin` (если не изменил `SHUBER_ADMIN_LOGIN`).

Открой **Admin / Music CMS**.

Добавь:

- название
- BPM
- описание
- MP3/OGG/WAV/M4A
- PNG/JPG/WEBP

После сохранения:

```text
R2
  ├─ data/shuber.json
  └─ media/
       ├─ audio/
       └─ covers/
```

## 7. Logs

В `wrangler.jsonc` уже включена:

```json
"observability": {
  "enabled": true
}
```

Поэтому ошибки Worker/Container можно смотреть в Cloudflare Logs и в разделе Containers.
