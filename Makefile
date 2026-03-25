.PHONY: build up down logs client clean

# Build OpenVPN image and backend
build:
	docker build -t otiv-openvpn ./openvpn
	docker compose build backend

# Start all services
up: build
	docker compose up -d

# Stop all services (backend SIGTERM이 동적 컨테이너를 정리; 실패 시 fallback)
down:
	docker compose down
	@docker rm -f $$(docker ps -aq --filter label=com.otiv.instance=true) 2>/dev/null || true

# Follow logs
logs:
	docker compose logs -f

# Build client binaries (requires Go)
client:
	cd client && go mod tidy
	cd client && go build -o ../bin/otiv-client  ./cmd/otiv-client
	cd client && go build -o ../bin/otiv-proxy   ./cmd/otiv-proxy

# Remove containers, volumes, and built images
clean:
	docker compose down -v
	rm -rf ./data
	docker rmi otiv-openvpn 2>/dev/null || true

# Run backend locally for development (requires Go, Docker socket)
dev-backend:
	cd backend && go mod tidy && DATA_DIR=/tmp/otiv-data go run .
