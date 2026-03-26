.PHONY: build up down logs client clean dev-backend

GO_IMAGE := golang:1.25-alpine
ROOT     := $(shell pwd)

# Build OpenVPN image and backend
build:
	docker build -t otiv-openvpn ./openvpn
	docker compose build backend

# Start all services
up: build
	docker compose up -d

# Stop all services
down:
	docker compose down
	@docker rm -f $$(docker ps -aq --filter label=com.otiv.instance=true) 2>/dev/null || true

# Follow logs
logs:
	docker compose logs -f

# Build client binary via Go container (no host Go required)
client:
	docker run --rm \
		-v "$(ROOT)/client":/src \
		-w /src \
		-e CGO_ENABLED=0 \
		$(GO_IMAGE) \
		sh -c "go mod tidy && go build -o /src/otiv-client ./cmd/otiv-client"
	@echo "Built: client/otiv-client"

# Run backend locally for development via Go container
dev-backend:
	docker run --rm -it \
		-v "$(ROOT)/backend":/src \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v "$(ROOT)/data":/data \
		-w /src \
		-p 8080:8080 \
		-e HOST_DATA_DIR=$(ROOT)/data \
		$(GO_IMAGE) \
		sh -c "go mod tidy && go run . -config /src/config.yaml.example 2>/dev/null || go run ."

# Remove containers, volumes, and built images
clean:
	docker compose down -v
	rm -rf ./data client/otiv-client
	docker rmi otiv-openvpn 2>/dev/null || true
