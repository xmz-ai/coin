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

Use bootstrap credentials configured in server env:

- `ADMIN_BOOTSTRAP_USERNAME` (default `admin`)
- `ADMIN_BOOTSTRAP_PASSWORD` (default `admin123456`)

## MVP Pages

- `/login`
- `/dashboard`
- `/merchants`
- `/customers`
- `/accounts`
- `/transactions`
- `/notify`
