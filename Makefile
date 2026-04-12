SHELL := /bin/zsh
ENV := source .venv/bin/activate
PY := $(ENV) && python manage.py
PACKAGE ?= $(shell bash -c 'read -p "Package name: " package; echo $$package')
BRANCH ?= $(shell bash -c 'read -p "Branch name: " branch; echo $$package')
MSG ?= $(shell bash -c 'read -p "What is the commit message?: " commit message; echo $$commit message')
ENVIRONMENT := $(if $(PRODUCTION_SERVER),prod,dev)
PROJECT_NAME := be_voxprofit

ifeq ($(ENVIRONMENT),prod)
	DOCKER_COMPOSE := docker compose -f container/docker-compose.prod.yml
else
	DOCKER_COMPOSE := docker compose -f container/docker-compose.yml
endif

# App Services
SERVICES := redis db gate migrator nginx ws graphql nats-1 nats-2 nats-3 sync cdc-worker fignode protocol

# Allow passing service names as arguments, e.g., "make rebuild nginx" or "make restart nginx"
ifneq ($(filter rebuild restart,$(firstword $(MAKECMDGOALS))),)
  RUN_ARGS := $(wordlist 2,$(words $(MAKECMDGOALS)),$(MAKECMDGOALS))
  $(eval $(RUN_ARGS):;@:)
endif

.PHONY: deploy up-scanner down-scanner build-scanner
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

up: create_networks
	$(DOCKER_COMPOSE) up --remove-orphans $(SERVICES)
upd: create_networks
	$(DOCKER_COMPOSE) up -d --build --remove-orphans $(SERVICES)

getlogs:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) logs -f $$SER_NAME


ssl:
	docker-compose -f container/docker-compose.ssl.yml up -d

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
	$(DOCKER_COMPOSE) exec db psql -U $$DB_USER -d db



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
	docker builder prune -f && docker system prune --volumes -f

install_make:
	apt-get update && apt-get install -y zsh && chsh -s $(which zsh) root && make install && uv pip install --upgrade pip


migrate:
	$(DOCKER_COMPOSE) up migrator

sqlc:
	~/go/bin/sqlc generate && make migrate

test:
	cd go && go test ./...

clean_db:
	$(DOCKER_COMPOSE) down
	rm -rf container/postgres/db_data
	$(MAKE) upd
	cd go && go build -o ../bin/store-webhook-secret ./cmd/store-webhook-secret/main.go && .. && ./bin/store-webhook-secret

scr:
	scrcpy --window-title "iPhone"

vndr:
	cd go && GOWORK=off go mod tidy && GOWORK=off go mod vendor
	cd tap && GOWORK=off go mod tidy && GOWORK=off go mod vendor

rebuild_all:
	$(MAKE) vndr && $(MAKE) down && $(MAKE) upd && $(MAKE) logs


rebuild: fix-permissions
	@if [ -n "$(RUN_ARGS)" ]; then \
		$(DOCKER_COMPOSE) up --build -d --force-recreate $(RUN_ARGS); \
	else \
		echo "Enter the service name: "; \
		read SER_NAME; \
		$(DOCKER_COMPOSE) up --build -d --force-recreate $$SER_NAME; \
	fi

ingest-messy:
	curl -v -X POST \
		-H "Authorization: Bearer $(TOKEN)" \
		-F "file=@/Users/Yankz/Downloads/Messy Bank Transactions - Generating Messy Bank Transaction Data.csv" \
		http://localhost:8080/files/upload/accounting/cleanup

nats_consumers:
	./container/scripts/list-nats-consumers.sh 