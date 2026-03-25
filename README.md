# OTIV — One-Time Isolated VPN

GUID 기반 WebSocket 터널 위에서 동작하는 일회성 격리 VPN 서버 관리 시스템.

## 개념

```text
[Client PC]
  openvpn (tcp-client) → localhost:11194
                              ↑
                         otiv-client (TCP↔WSS bridge)
                              ↓
                    ws://server/vpn/{guid}
                              ↓
                    [Server PC] Go Backend
                                    ↓
                     Docker: otiv-vpn-{id}:1194 (OpenVPN TCP)
```

- 서버는 VPN 인스턴스를 생성할 때마다 GUID 기반 WSS 경로를 발급한다.
- 각 인스턴스는 독립된 Docker 컨테이너로 실행되며 C 클래스 서브넷이 서로 다르다.
- 클라이언트는 GUID URL만 알면 별도 인증 없이 접속할 수 있다.

## 구성 요소

| 컴포넌트 | 기술 | 역할 |
| --- | --- | --- |
| `backend` | Go | VPN 인스턴스 관리 REST API + WebSocket 프록시 + 프론트엔드 프록시 |
| `frontend` | Vite + React | 인스턴스/클라이언트 현황 대시보드 |
| `openvpn` | OpenVPN (Docker) | 인스턴스별 TCP VPN 서버 |
| `otiv-client` | Go | 프록시 + OpenVPN 자동 실행 |
| `otiv-proxy` | Go | TCP↔WSS 브리지 단독 실행 |

## 빠른 시작

### 서버

```bash
# 의존성 준비
cd backend && go mod tidy && cd ..

# OpenVPN 이미지 빌드 + 전체 서비스 실행
make up

# 로그 확인
make logs
```

서비스가 올라오면 `http://localhost:8000` 에서 대시보드에 접속한다.

### 클라이언트 바이너리 빌드

```bash
make client
# → ./bin/otiv-client   (프록시 + OpenVPN 자동 실행)
# → ./bin/otiv-proxy    (프록시만 단독 실행)
```

### 클라이언트 접속 (otiv-client)

1. 대시보드에서 VPN 인스턴스를 생성한다.
2. 인스턴스 ID(GUID)를 클라이언트 PC로 전달한다.
3. 클라이언트 PC에서:

```bash
# .ovpn 자동 다운로드 + 프록시 시작 + openvpn 실행까지 한 번에
sudo ./otiv-client -url ws://SERVER_HOST:8000/vpn/{guid}

# .ovpn 파일을 직접 지정하려면
sudo ./otiv-client -url ws://SERVER_HOST:8000/vpn/{guid} -config ./client.ovpn
```

`openvpn` 바이너리가 설치되어 있어야 합니다 (`apt install openvpn`).

### 프록시만 사용 (otiv-proxy)

OpenVPN 연결을 직접 제어하고 싶거나 다른 VPN 클라이언트를 쓸 때:

```bash
./otiv-proxy -url ws://SERVER_HOST:8000/vpn/{guid} -port 11194
# → localhost:11194 로 OpenVPN 클라이언트를 연결
```

## API

| Method | Path | 설명 |
| --- | --- | --- |
| `GET` | `/api/instances` | 인스턴스 목록 (접속 클라이언트 포함) |
| `POST` | `/api/instances` | 인스턴스 생성 `{"name": "..."}` |
| `DELETE` | `/api/instances/{id}` | 인스턴스 삭제 |
| `GET` | `/api/instances/{id}/clients` | 접속 중인 클라이언트 목록 |
| `GET` | `/api/instances/{id}/client-config` | .ovpn 파일 다운로드 |
| `WS` | `/vpn/{id}` | VPN WebSocket 프록시 엔드포인트 |

## WebSocket 터널 프로토콜

클라이언트 스크립트를 다른 언어로 포팅할 때 참고:

- 연결: `ws(s)://host/vpn/{guid}` 에 WebSocket 연결
- 프레임: **Binary** 프레임만 사용
- 내용: OpenVPN TCP 스트림의 raw 바이트를 그대로 전달 (별도 framing 없음)
- 방향: 양방향 (full-duplex)

## 환경변수 (backend)

| 변수 | 기본값 | 설명 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP 리스닝 주소 |
| `DATA_DIR` | `/data` | PKI/인스턴스 데이터 저장 경로 |
| `DOCKER_NETWORK` | `otiv_network` | 백엔드/프론트엔드 Docker 네트워크 |
| `VPN_NETWORK` | `otiv_vpn_net` | OpenVPN 컨테이너 전용 internal 네트워크 |
| `OVPN_IMAGE` | `otiv-openvpn` | OpenVPN Docker 이미지 이름 |
| `FRONTEND_URL` | `http://frontend:5173` | 프론트엔드 프록시 대상 URL |

## 디렉토리 구조

```text
otiv/
├── backend/              # Go 백엔드
│   └── internal/
│       ├── config/       # 환경변수 로드
│       ├── vpn/          # PKI, 인스턴스 구조체, Docker 기반 매니저
│       ├── api/          # HTTP 핸들러 (REST + WS + 프론트엔드 프록시)
│       └── proxy/        # WebSocket ↔ TCP 브리지
├── openvpn/              # OpenVPN Alpine 이미지
├── client/               # 클라이언트 (Go)
│   ├── cmd/otiv-client/ # 프록시 + OpenVPN 자동 실행
│   ├── cmd/otiv-proxy/  # TCP↔WSS 브리지 단독
│   └── internal/bridge/  # 공통 브리지 로직
├── frontend/             # Vite + React 대시보드

├── docker-compose.yml
└── Makefile
```

## 주의사항

- GUID URL이 곧 접속 권한이다. 외부에 노출되지 않도록 주의한다.
- `/var/run/docker.sock` 마운트가 필요하다 (사이드카 컨테이너 생성용).
- 서버에 `/dev/net/tun` 장치가 있어야 한다.
- OpenVPN 컨테이너는 `internal` 네트워크에만 연결되어 호스트 PC로의 직접 접근이 차단된다.
- 파일럿 수준의 구현으로, 프로덕션 사용을 상정하지 않는다.
