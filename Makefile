SHELL := /bin/zsh
ENV := source .venv/bin/activate
PY := $(ENV) && python manage.py
PACKAGE ?= $(shell bash -c 'read -p "Package name: " package; echo $$package')
BRANCH ?= $(shell bash -c 'read -p "Branch name: " branch; echo $$package')
MSG ?= $(shell bash -c 'read -p "What is the commit message?: " commit message; echo $$commit message')
ENVIRONMENT := $(if $(PRODUCTION_SERVER),prod,dev)
PROJECT_NAME := be_voxprofit

ifeq ($(ENVIRONMENT),prod)
	DOCKER_COMPOSE := docker-compose -f container/docker-compose.prod.yml
else
	DOCKER_COMPOSE := docker-compose -f container/docker-compose.yml
endif

# App Services
SERVICES := app-django redis db svix-server

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

proj:
	django-admin startproject config . && $(PY) && python manage.py startapp users

app:
	@echo "Enter the app name: "; \
	read APP_NAME; \
	mkdir -p $${APP_NAME}-be_py/$${APP_NAME} && \
	echo "# $${APP_NAME}" > $${APP_NAME}-be_py/README.md && \
	echo " " >> $${APP_NAME}-be_py/pyproject.toml && \
	echo "urlpatterns = []" >> $${APP_NAME}-be_py/$${APP_NAME}/urls.py && \
	python manage.py startapp $$APP_NAME $${APP_NAME}-be_py/$${APP_NAME}

super:
	@echo "Enter the username: "; \
	read USERNAME; \
	python manage.py createsuperuser  && python manage.py changepassword $$USERNAME

static:
	$(PY) collectstatic

clean:
	git commit -am "clean up" && git push

http:
	ngrok http 3000

logs:
	$(DOCKER_COMPOSE) logs -f $(ARGS)

svix-logs:
	$(DOCKER_COMPOSE) logs -f svix-server

django-logs:
	$(DOCKER_COMPOSE) logs -f app-django

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


manage:
	$(DOCKER_COMPOSE) exec app-django sh -c "cd fignode && python manage.py $(ARGS)"

exec:
	$(DOCKER_COMPOSE) exec $(ARGS)

envs_used:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) exec "$$SER_NAME" env


rebuild: fix-permissions
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) up --build -d --force-recreate $$SER_NAME

build: create_networks
	$(DOCKER_COMPOSE) build $(SERVICES)

build_no_cache: create_networks
	$(DOCKER_COMPOSE) build --no-cache $(SERVICES)

up: create_networks
	$(DOCKER_COMPOSE) up --remove-orphans $(SERVICES)
upd: create_networks
	$(DOCKER_COMPOSE) up -d --build --remove-orphans $(SERVICES)

ssl:
	docker-compose -f container/docker-compose.ssl.yml up -d

fix-permissions:
	@if [ "$(ENVIRONMENT)" = "prod" ]; then \
		echo "Running fix-permissions on production server"; \
		echo "$(DO_CD_SUDO_PASSWORD)" | sudo -S chown -R $$(whoami):$$(whoami) container/postgres/db_data; \
	else \
		echo "Skipping fix-permissions as this is not a production server"; \
	fi

up-tts: create_networks
	$(DOCKER_COMPOSE) up -d --build dia_tts

down:
	$(DOCKER_COMPOSE) down --remove-orphans

restart:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	$(DOCKER_COMPOSE) restart $$SER_NAME

psql:
	@echo "Enter DB_USER: "; \
	read DB_USER; \
	$(DOCKER_COMPOSE) exec db psql -U $$DB_USER -d db

test:
	$(DOCKER_COMPOSE) exec app-django python manage.py test $(PARAMETER)


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



install:
	pip install -e . --config-settings editable_mode=strict

be_py:
	git clone --depth 1 --branch main git@github.com:zeed-ma/zeed-be_py.git

clone:
	@echo "Enter the app name: "; \
	read APP_NAME; \
	git clone --depth 1 --branch main git@github.com:zeed-ma/zeed-be-$${APP_NAME}_py.git
	
prune:
	docker system prune -a --volumes -f && docker volume prune -f && docker network prune -f && sudo systemctl restart docker

supervisor:
	$(DOCKER_COMPOSE) exec app-django supervisorctl restart all
	
restart_celery:
	$(DOCKER_COMPOSE) exec app-django supervisorctl restart celery

install_make:
	apt-get update && apt-get install -y zsh && chsh -s $(which zsh) root && make install && uv pip install --upgrade pip





make_migration:
	$(DOCKER_COMPOSE) exec app-django python manage.py makemigrations

migrate:
	$(DOCKER_COMPOSE) exec app-django python manage.py migrate

showmigrations:
	$(DOCKER_COMPOSE) exec app-django python manage.py showmigrations 

n8n_up:
	docker-compose -f compose/docker-compose.n8n.yml up -d n8n
	ngrok http --url=prime-legible-turkey.ngrok-free.app 5678  

n8n_down:
	docker-compose -f compose/docker-compose.n8n.yml down
n8n_logs:
	@echo "Enter the service name: "; \
	read SER_NAME; \
	docker-compose -f compose/docker-compose.n8n.yml exec n8n tail -f /home/node/.n8n/$(SER_NAME).log

commit:
	@echo "Enter commit message: "; \
	read MSG; \
	CUR_BRANCH=$$(git rev-parse --abbrev-ref HEAD); \
	git add -u && git commit -m "$$MSG" && git push origin "$$CUR_BRANCH"

# Webhook Bridge Commands
bridge:
	$(DOCKER_COMPOSE) exec app-django python manage.py run_webhook_bridge

bridge-logs:
	$(DOCKER_COMPOSE) logs -f app-django | grep "webhook-bridge"
