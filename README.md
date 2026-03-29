# OTIV — One-Time Isolated VPN

GUID 기반 WebSocket 터널 위에서 동작하는 격리 VPN 서버 관리 시스템.

## 개념

```text
[Client PC]
  openvpn (tcp-client) → localhost:11194
                              ↑
                         otiv-client (TCP↔WSS bridge)
                              ↓
                    https://server/vpn/{guid}
                              ↓
                    [Server PC] Go Backend
                                    ↓
                     Docker: otiv-vpn-{id}:1194 (OpenVPN TCP)
```

- 서버는 VPN 인스턴스를 생성할 때마다 GUID 기반 WebSocket 경로를 발급한다.
- 각 인스턴스는 독립된 Docker 컨테이너로 실행되며 C 클래스 서브넷이 서로 다르다.
- 프론트엔드 진입 시 접속 비밀번호가 요구된다. 관리자 기능은 별도 관리자 비밀번호로 잠긴다.

## 구성 요소

| 컴포넌트 | 기술 | 역할 |
| --- | --- | --- |
| `backend` | Go | VPN 인스턴스 관리 REST API + WebSocket 프록시 + 프론트엔드 프록시 |
| `frontend` | Vite + React | 인스턴스/클라이언트 현황 대시보드 |
| `openvpn` | OpenVPN (Docker) | 인스턴스별 TCP VPN 서버 |
| `otiv-client` | Go | 프록시 + OpenVPN 자동 실행 |

## 빠른 시작

### 서버

```bash
make up      # OpenVPN 이미지 빌드 + 전체 서비스 실행
make logs    # 로그 확인
```

서비스가 올라오면 `http://localhost:8000` 에서 대시보드에 접속한다.

초기 실행 시 `/data/config.yaml` 이 자동 생성된다. 비밀번호를 변경한 뒤 `docker compose restart backend` 로 반영한다.

### 클라이언트 바이너리 빌드

```bash
make client
# → ./bin/otiv-client        (Linux/macOS)
# → ./bin/otiv-client.exe    (Windows cross-compile)
```

### 클라이언트 접속

1. 대시보드에서 VPN 인스턴스를 생성한다.
2. Connect 섹션을 펼쳐 명령어를 복사한다 (Windows / Linux·macOS 탭 제공).
3. 클라이언트 PC에서 실행:

```bash
# Linux / macOS — .ovpn 자동 다운로드 + 프록시 + openvpn 한 번에
sudo otiv-client connect https://SERVER_HOST/vpn/{guid}

# Windows (PowerShell, 관리자 권한)
otiv-client.exe connect https://SERVER_HOST/vpn/{guid}

# 프로토콜 생략도 동작 (wss:// 로 자동 정규화)
sudo otiv-client connect SERVER_HOST/vpn/{guid}
```

`openvpn` 바이너리가 설치되어 있어야 한다 (`apt install openvpn` / Windows: OpenVPN 공식 설치 프로그램).

#### proxy 모드 (OpenVPN 직접 제어)

```bash
otiv-client proxy https://SERVER_HOST/vpn/{guid}
# → localhost:11194 로 OpenVPN 클라이언트 연결 가능
# → localhost:8080  HTTP CONNECT 프록시
```

#### DNS (Linux / macOS 전용)

```bash
otiv-client dns list  https://SERVER_HOST/vpn/{guid}
sudo otiv-client dns apply https://SERVER_HOST/vpn/{guid}
```

## 인증 및 접근 제어

| 기능 | 방법 |
| --- | --- |
| 일반 접속 | 프론트엔드 진입 시 `access_password` 입력 |
| 관리자 모드 | 로고를 1000ms 안에 5번 클릭 → `admin_password` 입력 |
| 세션 격리 | `sessionStorage` 사용 — 탭마다 독립 로그인 필요 |
| IP 자동 차단 | 로그인 실패 10회 → IP 자동 차단 |

## 관리자 기능

- **인스턴스 생성 / 삭제**
- **클라이언트 Kick**: CCD `disable` 파일 작성 + WebSocket 즉시 종료 → 재접속 차단
- **클라이언트 Ban**: 실제 HTTP 클라이언트 IP를 차단 목록에 추가 + 활성 세션 즉시 종료
- **IP 차단 관리**: 차단 목록 조회 / 수동 추가 / 해제 (`/data/blocked_ips.json` 기반, 파일 삭제 시 초기화)
- **접속 시간제한**: 전역(`connection_timeout`) 및 클라이언트별 개별 설정 (0 이하 = 무제한)
- **최대 동시 접속 수**: 전역(`max_clients_per_instance`) 및 인스턴스별 개별 설정 (0 이하 = 무제한) — WebSocket 업그레이드 전에 검사하므로 지연 없이 즉시 차단
- **Remote IP 표시**: 관리자에게만 노출 (백엔드에서 필드 제거)

## API

| Method | Path | Auth | 설명 |
| --- | --- | --- | --- |
| `POST` | `/api/auth` | — | 로그인 `{"password": "..."}` → `{"token", "role"}` |
| `GET` | `/api/instances` | user | 인스턴스 목록 (클라이언트 포함) |
| `POST` | `/api/instances` | admin | 인스턴스 생성 `{"name": "..."}` |
| `DELETE` | `/api/instances/{id}` | user | 인스턴스 삭제 |
| `POST` | `/api/instances/{id}/stop` | user | 인스턴스 중지 |
| `POST` | `/api/instances/{id}/start` | user | 인스턴스 시작 |
| `GET` | `/api/instances/{id}/clients` | user | 접속 클라이언트 목록 |
| `GET` | `/api/instances/{id}/client-config` | — | .ovpn 파일 다운로드 (UUID가 접근 제어) |
| `POST` | `/api/instances/{id}/clients/{cn}/kick` | admin | 클라이언트 kick + 재접속 차단 |
| `PUT` | `/api/instances/{id}/clients/{cn}/timeout` | admin | 클라이언트별 시간제한 설정 (초, 0 이하=전역) |
| `PUT` | `/api/instances/{id}/max-clients` | admin | 인스턴스별 최대 접속 수 (0 이하=전역) |
| `PUT` | `/api/instances/{id}/hostnames/{cn}` | user | 클라이언트 hostname 설정 |
| `GET` | `/api/blocked` | admin | 차단 IP 목록 |
| `POST` | `/api/blocked` | admin | IP 차단 |
| `DELETE` | `/api/blocked/{ip}` | admin | IP 차단 해제 |
| `WS` | `/vpn/{id}` | — | VPN WebSocket 프록시 |
| `WS` | `/ws-tcp` | — | TCP relay (HTTP CONNECT용) |
| `GET` | `/download/{file}` | — | 클라이언트 바이너리 다운로드 |

## HTTPS / TLS

`/data/tls/cert.pem` 과 `/data/tls/key.pem` 이 존재하면 자동으로 HTTPS 모드로 동작한다.
인증서가 없거나 유효하지 않으면 HTTP 로 폴백하고 로그를 남긴다.

```
data/
└── tls/
    ├── cert.pem   # 인증서 (체인 포함 가능)
    └── key.pem    # 개인키
```

## 데이터 디렉토리 구조

```text
data/
├── config.yaml          # 서버 설정 (초기 실행 시 자동 생성)
├── blocked_ips.json     # 차단 IP 목록 (삭제 시 차단 목록 초기화)
├── instances.json       # 인스턴스 상태 (자동 관리)
├── pki/                 # CA 및 인증서
└── instances/
    └── {guid}/
        ├── server.conf  # OpenVPN 서버 설정
        ├── ca.crt
        ├── server.crt / server.key
        ├── dnsmasq.hosts
        └── ccd/         # 클라이언트별 설정 (kick 시 disable 파일 생성)
```

## WebSocket 터널 프로토콜

다른 언어로 클라이언트를 구현할 때 참고:

- 연결: `ws(s)://host/vpn/{guid}` 에 WebSocket 연결 (http/https/bare host도 자동 정규화)
- 프레임: **Binary** 프레임만 사용
- 내용: OpenVPN TCP 스트림의 raw 바이트를 그대로 전달 (별도 framing 없음)
- 방향: 양방향 (full-duplex)

## 디렉토리 구조

```text
otiv/
├── backend/
│   └── internal/
│       ├── config/       # YAML 설정 로드
│       ├── vpn/          # PKI, 인스턴스, Docker 매니저, timeout enforcer
│       ├── api/          # HTTP 핸들러, IP blocker, WebSocket 연결 추적
│       └── proxy/        # WebSocket ↔ TCP 브리지
├── openvpn/              # OpenVPN Alpine 이미지
├── client/
│   └── cmd/otiv-client/  # 프록시 + OpenVPN 자동 실행
├── frontend/             # Vite + React 대시보드
├── data/                 # 런타임 데이터 (config.yaml, pki, instances)
├── docker-compose.yml
└── Makefile
```

## 주의사항

- GUID URL이 곧 VPN 접속 권한이다. 외부에 노출되지 않도록 주의한다.
- `/var/run/docker.sock` 마운트가 필요하다 (사이드카 컨테이너 생성용).
- 서버에 `/dev/net/tun` 장치가 있어야 한다.
- OpenVPN 컨테이너는 `internal` 네트워크에만 연결되어 호스트로의 직접 접근이 차단된다.
