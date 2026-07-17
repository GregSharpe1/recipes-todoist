APP_NAME ?= todoist-recipes
IMAGE ?= $(APP_NAME):latest
PORT ?= 8080
COMPOSE ?= docker compose

.PHONY: build run docker-build docker-run docker-stop compose-up compose-down compose-logs

build:
	go build ./...

run:
	go run .

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker run --rm -p $(PORT):8080 --env DATABASE_PATH --env TODOIST_API_TOKEN --env TODOIST_PROJECT_ID --env TODOIST_PROJECT --env TODOIST_API_BASE_URL --env BASE_URL --env LOCAL_IP -v $(CURDIR)/data:/app/data -v $(CURDIR)/uploads:/app/uploads $(IMAGE)

docker-stop:
	docker ps -q --filter ancestor=$(IMAGE) | xargs -r docker stop

compose-up:
	$(COMPOSE) up -d --build

compose-down:
	$(COMPOSE) down

compose-logs:
	$(COMPOSE) logs -f app
