# Implementation Plan - Dockerization

This plan outlines the steps to containerize the Toro application, setting up Go, Django, Redis, and Postgres services using Docker Compose.

## User Review Required
- [ ] Confirm Django project root location.
- [ ] Review environment variable strategy (using `.env` file).

## Proposed Changes

### Configuration
#### [NEW] [docker-compose.yml](file:///Users/Yankz/programming/usetoro/docker-compose.yml)
- Define 4 services:
    - `db`: Image `postgres:15-alpine`. Environment variables for user/password/db. Volume for persistence.
    - `redis`: Image `redis:alpine`.
    - `app-go`: Build from `./go`. Expose port 8080. Depends on `redis`, `db`.
    - `app-django`: Build from `./`. Expose port 8000. Depends on `redis`, `db`.
- Define networks and volumes (including `postgres_data`).

### Go Component
#### [NEW] [go/Dockerfile](file:///Users/Yankz/programming/usetoro/go/Dockerfile)
- Multi-stage build for the Go binary.
- Stage 1: Builder (Go 1.24).
- Stage 2: Runtime (Alpine).

### Django Component
#### [NEW] [Dockerfile](file:///Users/Yankz/programming/usetoro/Dockerfile)
- Python 3.11+ environment.
- Install dependencies from `requirements.txt`.
- Copy source code.
- Command to run `gunicorn` or `python manage.py runserver 0.0.0.0:8000`.

## Verification Plan
### Automated Tests
- Run `docker-compose up --build -d`.
- Verify all 4 containers are running: `docker-compose ps`.
- Test Go endpoint: `curl localhost:8080/hooks/stripe`.
- Test Django connection to DB (via startup logs or admin page).
