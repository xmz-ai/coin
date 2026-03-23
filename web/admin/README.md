# COIN Admin Web (MVP)

## Run

```bash
cd web/admin
npm install
npm run dev
```

Default API base:

- `http://127.0.0.1:8080/admin/api/v1`

Override with env var:

- `NEXT_PUBLIC_ADMIN_API_BASE`

## Login

On first startup, open `/setup` to initialize:

- admin username/password
- default merchant
- optional default merchant webhook URL (`https://...` only; leave empty to disable callback and skip outbox enqueue)
- merchant secret (shown in setup result)

## MVP Pages

- `/setup`
- `/setup/success`
- `/login`
- `/dashboard`
- `/merchants`
- `/customers`
- `/accounts`
- `/transactions`
- `/notify`
