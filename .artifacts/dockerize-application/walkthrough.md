# Dockerization Walkthrough

I have containerized the Toro application with Go, Django, Redis, and Postgres using a centralized `compose/` directory.

## File Structure
- `compose/docker-compose.yml`: Orchestrator.
- `compose/go/Dockerfile`: Multi-stage build for the Go "Muscle".
- `compose/django/Dockerfile`: Python environment for the Django "Brain".
- `requirements.txt`: Minimal Python dependencies (in root).

## How to Run

1. **Start Docker Desktop**: Ensure Docker is running.
2. **Build and Run**:
   ```bash
   docker-compose -f compose/docker-compose.yml up --build
   ```

## Services
| Service | Internal Port | Host Port | Description |
| :--- | :--- | :--- | :--- |
| **db** | 5432 | 5432 | Postgres 15 |
| **redis** | 6379 | 6379 | Redis Cache/Streams |
| **app-go** | 8080 | 8080 | Go Ingest API |
| **app-django** | 8000 | 8000 | Django App |

## Verification
Once running, you can test the endpoints:

**Go Ingest:**
```bash
curl -v http://localhost:8080/hooks/stripe
```

**Django Health:**
```bash
curl -I http://localhost:8000/admin/login/
```
