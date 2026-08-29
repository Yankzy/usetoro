SHELL := /bin/zsh
ENV := source .venv/bin/activate
PY := $(ENV) && python manage.py
PACKAGE ?= $(shell bash -c 'read -p "Package name: " package; echo $$package')
BRANCH ?= $(shell bash -c 'read -p "Branch name: " branch; echo $$package')
MSG ?= $(shell bash -c 'read -p "What is the commit message?: " commit message; echo $$commit message')
PRODUCTION_SERVER := 1
ENVIRONMENT := $(if $(PRODUCTION_SERVER),prod,dev)
PROJECT_NAME := usetoro

ifeq ($(ENVIRONMENT),prod)
	DOCKER_COMPOSE := docker compose -f container/docker-compose.prod.yml
else
	DOCKER_COMPOSE := docker compose -f container/docker-compose.yml
endif

DEPLOY_CONTEXT ?= droplet
DOCKER_CONTEXT := docker --context $(DEPLOY_CONTEXT) compose -f container/docker-compose.prod.yml

# App Services
SERVICES := redis torodb gate migrator nginx ws graphql nats-1 nats-2 nats-3 sync fignode protocol python-worker
OUR_SERVICES := torodb gate migrator nginx ws graphql sync fignode protocol python-worker

# Allow passing service names as arguments, e.g., "make rebuild nginx" or "make restart nginx"
ifneq ($(filter rebuild restart build_prod docker_context_prod_push deploy_second_mac build_run build_up two_stage,$(firstword $(MAKECMDGOALS))),)
  RUN_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  $(eval $(RUN_ARGS):;@:)
endif

.PHONY: deploy up-scanner down-scanner build-scanner vndr
deploy:
	@touch .deploy
	@git add .deploy
	@git commit -m "deploy"
	@git push origin HEAD
	@git rm .deploy
	@git commit -m "[skip ci] remove .deploy file"
	@git push origin HEAD

help:
	@$(MAKE) -pRrq -f $(lastword $(MAKEFILE_LIST)) : 2>/dev/null | awk -v RS= -F: '/^# File/,/^# Finished Make data base/ {if ($$1 !~ "^[#.]") {print $$1}}' | sort | egrep -v -e '^[^[:alnum:]]' -e '^$@$$'

venv:
	python3.12 -m venv .venv && $(ENV) && pip install --upgrade pip


static:
	$(PY) collectstatic

clean:
	git commit -am "clean up" && git push

ngrok:
	ngrok http --url=prime-legible-turkey.ngrok-free.app 80

logs:
	$(DOCKER_COMPOSE) logs -f $(ARGS)

svix_logs:
	$(DOCKER_COMPOSE) logs -f svix-server

ls:
	$(DOCKER_COMPOSE) run celery ls -la /app/

shell:
	$(DOCKER_COMPOSE) exec app-django python manage.py shell

bash:
	$(DOCKER_COMPOSE) exec app-django /bin/bash


supervisor_tail:
	$(DOCKER_COMPOSE) exec app-django /bin/bash -c "supervisorctl tail -f celery-beat stdout"

tail:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) exec app-django /bin/bash -c "tail -f .logs/$$SER_NAME.log & bash"

tails:
	$(DOCKER_COMPOSE) exec app-django /bin/bash -c "tail -f .logs/asgi.log & tail -f .logs/celery.log & bash"


exec:
	$(DOCKER_COMPOSE) exec $(ARGS)

envs_used:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) exec "$$SER_NAME" env

build: create_networks
	$(DOCKER_COMPOSE) build $(SERVICES)

build_no_cache: create_networks
	$(DOCKER_COMPOSE) build --no-cache $(SERVICES)

dev: create_networks
	$(DOCKER_COMPOSE) up --build --remove-orphans

up: 
	$(MAKE) start-colima
	$(MAKE) create_networks
	$(DOCKER_COMPOSE) up --remove-orphans $(SERVICES)

upd: 
	$(MAKE) start-colima
	$(MAKE) create_networks
	$(MAKE) vndr && $(DOCKER_COMPOSE) up -d --build --remove-orphans $(SERVICES) && $(MAKE) logs


build_run: create_networks
	$(MAKE) vndr
	$(DOCKER_COMPOSE) build $(if $(RUN_ARGS),$(RUN_ARGS),$(SERVICES))
	$(DOCKER_COMPOSE) up -d --remove-orphans $(if $(RUN_ARGS),$(RUN_ARGS),$(SERVICES))
	$(MAKE) logs


getlogs:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) logs -f $$SER_NAME


ssl:
	docker compose -f container/docker-compose.ssl.yml up -d

fix-permissions:
	@if [ "$(ENVIRONMENT)" = "prod" ]; then \
		echo "Running fix-permissions on production server"; \
		echo "$(DO_CD_SUDO_PASSWORD)" | sudo -S chown -R $$(whoami):$$(whoami) container/postgres/db_data; \
	else \
		echo "Skipping fix-permissions as this is not a production server"; \
	fi

down:
	$(DOCKER_COMPOSE) down --remove-orphans

restart:
	@if [ -n "$(RUN_ARGS)" ]; then \
		$(DOCKER_COMPOSE) restart $(RUN_ARGS); \
	else \
		echo "Enter the service name: "; \
		read SER_NAME; \
		$(DOCKER_COMPOSE) restart $$SER_NAME; \
	fi

psql:
	@echo "Enter DB_USER: "; \
	read DB_USER; \
	$(DOCKER_COMPOSE) exec torodb psql -U $$DB_USER -d toro



submodule:
	git submodule init && git submodule update

pull:
	sudo rm -rf fignode-be_py
	git clone --recurse-submodules git@github.com:Yankzy/fignode-be_py.git
staging:
	git pull origin staging && git submodule foreach 'git checkout master && git pull origin master'

squp:
	$(DOCKER_COMPOSE) up sonarqube
sqdown:
	$(DOCKER_COMPOSE) down sonarqube

scanner:
	$(DOCKER_COMPOSE) stop sonarscanner
	$(DOCKER_COMPOSE) up --force-recreate sonarscanner
	
create_networks:
	@if docker network ls | grep -q "toro-net"; then \
		echo "\033[1;32mtoro-net EXISTS, SKIPPING CREATION.\033[0m"; \
	else \
		echo "\033[1;33mCREATING NETWORK toro-net...\033[0m"; \
		docker network create toro-net; \
	fi

	
prune:
	$(MAKE) start-colima
	docker builder prune -f && docker system prune --volumes -f

install_make:
	apt-get update && apt-get install -y zsh && chsh -s $(which zsh) root && make install && uv pip install --upgrade pip


migrate:
	$(MAKE) start-colima
	$(DOCKER_COMPOSE) run --rm migrator

sqlc:
	~/go/bin/sqlc generate && $(MAKE) migrate

test:
	cd go && GOWORK=off go test -mod=vendor ./...

clean_db:
	$(DOCKER_COMPOSE) down -v
	rm -rf container/postgres/db_data
	rm -rf container/postgres/alloydb_data
	$(DOCKER_COMPOSE) up -d torodb
	$(DOCKER_COMPOSE) run --rm migrator
	~/go/bin/sqlc generate
	cd go && go build -o ../bin/store-webhook-secret ./cmd/store-webhook-secret/main.go && cd .. && ./bin/store-webhook-secret

scr:
	scrcpy --window-title "iPhone"

vndr:
	cd go && GOWORK=off go mod tidy && GOWORK=off go mod vendor
	cd tap && GOWORK=off go mod tidy && GOWORK=off go mod vendor
	go work vendor

# Preference should be given to rebuilding specific services (e.g., make rebuild gate)
rebuild_all:
	$(MAKE) vndr && $(MAKE) down && $(MAKE) upd && $(MAKE) logs


rebuild:
	colima start
	@if [ -n "$(RUN_ARGS)" ]; then \
		$(MAKE) down && $(MAKE) vndr && $(MAKE) sqlc && $(DOCKER_COMPOSE) build $(RUN_ARGS) && $(MAKE) up; \
	else \
		echo "Enter the service name: "; \
		read SER_NAME; \
		$(MAKE) down && $(MAKE) vndr && $(MAKE) sqlc && $(DOCKER_COMPOSE) up --build --force-recreate $$SER_NAME; \
	fi

ingest-messy:
	curl -v -X POST \
		-H "Authorization: Bearer $(TOKEN)" \
		-F "file=@/Users/Yankz/Downloads/Messy Bank Transactions - Generating Messy Bank Transaction Data.csv" \
		http://localhost:8080/files/upload/accounting/cleanup

nats_consumers:
	./container/scripts/list-nats-consumers.sh 


build_prod:
	docker compose -f container/docker-compose.prod.yml build $(if $(RUN_ARGS),$(RUN_ARGS),$(OUR_SERVICES))





docker_context_prod_push:
	colima start
	docker compose -f container/docker-compose.prod.yml push $(if $(RUN_ARGS),$(RUN_ARGS),$(OUR_SERVICES))

docker_context_prod_up:
	$(DOCKER_CONTEXT) pull
	$(DOCKER_CONTEXT) up -d
docker_context_prod_down:
	$(DOCKER_CONTEXT) down
	$(MAKE) docker_context_prod_prune

docker_context_prod_delete_db:
	$(DOCKER_CONTEXT) down -v
	$(MAKE) docker_context_prod_prune

docker_context_prod_prune:
	docker --context $(DEPLOY_CONTEXT) builder prune -f
	docker --context $(DEPLOY_CONTEXT) system prune --volumes -f


deploy_second_mac: 
	@echo "Copying compose and env files to second Mac..."
	scp container/docker-compose.prod.yml .env Makefile clipboard.txt yankz@yankz.local:~/
	@echo "Triggering pull and run on second Mac..."
	ssh yankz@yankz.local 'export PATH="/opt/homebrew/bin:/usr/local/bin:$$PATH" && \
		docker compose -f ~/docker-compose.prod.yml --env-file ~/.env pull $(if $(RUN_ARGS),$(RUN_ARGS),$(OUR_SERVICES)) && \
		docker compose -f ~/docker-compose.prod.yml --env-file ~/.env up -d $(RUN_ARGS)'

ssh_mac:
	ssh yankz@yankz.local

copy_clipboard_to_mac:
	scp clipboard.txt yankz@yankz.local:~/

# Deploy everything to second Mac AND stop protocol there so local dev takes over
deploy_infra_second_mac: deploy_second_mac
	@echo "Stopping remote protocol container on second Mac..."
	ssh yankz@yankz.local 'docker compose -f ~/docker-compose.prod.yml stop protocol'

# Run protocol locally connected to second Mac infrastructure (native Go)
dev_protocol_local:
	NATS_URL="nats://yankz.local:4222" \
	DATABASE_URL="postgres://toro:toro_password@yankz.local:5435/toro?sslmode=disable&options=-c%20search_path=toro_core,shadow_erp,fignode,public" \
	REDIS_URL="redis://yankz.local:6380" \
	go run ./go/cmd/protocol/main.go

# Run protocol in local Docker container with Docker Compose logs connected to second Mac infrastructure
dev_protocol_docker: create_networks
	$(DOCKER_COMPOSE) down
	NATS_URL="nats://yankz.local:4222" \
	DATABASE_URL="postgres://toro:toro_password@yankz.local:5435/toro?sslmode=disable&options=-c%20search_path=toro_core,shadow_erp,fignode,public" \
	REDIS_URL="redis://yankz.local:6380" \
	docker compose -f container/docker-compose.yml up --build --no-deps protocol


start-colima:
# 6 cpu, 10gb mem, 100gb disk, mount-inotify
	colima start --cpu 6 --memory 10 --disk 100 --mount-inotify && docker context use colima

reset-colima:
	colima delete && colima start --cpu 6 --memory 10 --disk 100 --mount-inotify

stop-colima:
	colima stop


colima-status:
	colima status


delete-colima:
	colima delete
