package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/otiv/backend/internal/api"
	"github.com/otiv/backend/internal/config"
	"github.com/otiv/backend/internal/vpn"
)

func main() {
	configPath := flag.String("config", "/data/config.yaml", "path to YAML config file")
	flag.Parse()

	// config 파일이 없으면 기본값으로 생성, 있으면 권한 보장
	if _, err := os.Stat(*configPath); os.IsNotExist(err) {
		if err := config.WriteDefaults(*configPath); err != nil {
			log.Printf("warning: 기본 config 파일 생성 실패 (%s): %v", *configPath, err)
		} else {
			log.Printf("기본 config 파일 생성됨: %s — 필요 시 수정 후 재시작하세요", *configPath)
		}
	} else {
		os.Chmod(*configPath, 0666)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// TLS 디렉토리 생성 (없으면 자동 생성)
	tlsDir := filepath.Join(cfg.DataDir, "tls")
	if err := os.MkdirAll(tlsDir, 0700); err != nil {
		log.Printf("warning: TLS 디렉토리 생성 실패 %s: %v", tlsDir, err)
	}

	// TLS 인증서 경로 결정 (config 미지정 시 data_dir/tls/ 자동 탐색)
	certPath := cfg.TLS.Cert
	keyPath := cfg.TLS.Key
	if certPath == "" {
		certPath = filepath.Join(cfg.DataDir, "tls", "cert.pem")
	}
	if keyPath == "" {
		keyPath = filepath.Join(cfg.DataDir, "tls", "key.pem")
	}

	// 인증서 체인 검증
	useTLS := false
	if err := validateCertChain(certPath, keyPath); err != nil {
		log.Printf("TLS 비활성화: %v — HTTP 모드로 시작합니다", err)
	} else {
		useTLS = true
		log.Printf("TLS 인증서 확인됨: %s", certPath)
	}

	manager, err := vpn.NewManager(cfg)
	if err != nil {
		log.Fatalf("init manager: %v", err)
	}

	handler := api.NewHandler(manager, cfg.FrontendURL, cfg.AccessPassword, cfg.AdminPassword, cfg.DataDir, cfg.ConnectionTimeout)

	srv := &http.Server{
		Addr:    cfg.ListenAddr(),
		Handler: handler.Routes(),
	}

	go func() {
		if useTLS {
			log.Printf("listening on %s (HTTPS)", cfg.ListenAddr())
			err = srv.ListenAndServeTLS(certPath, keyPath)
		} else {
			log.Printf("listening on %s (HTTP)", cfg.ListenAddr())
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	s := <-quit
	log.Printf("signal %s — shutting down", s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)

	log.Printf("stopping all vpn instances...")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()
	manager.Shutdown(shutCtx)
	log.Printf("done")
}

// validateCertChain 은 TLS 인증서 파일과 키 파일을 로드하고 체인을 검증한다.
func validateCertChain(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return fmt.Errorf("cert 파일 없음: %s", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		return fmt.Errorf("key 파일 없음: %s", keyFile)
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("인증서 로드 실패: %w", err)
	}

	if len(cert.Certificate) == 0 {
		return fmt.Errorf("인증서 체인이 비어있음")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("인증서 파싱 실패: %w", err)
	}

	now := time.Now()
	if now.Before(leaf.NotBefore) {
		return fmt.Errorf("인증서 아직 유효하지 않음 (유효 시작: %v)", leaf.NotBefore)
	}
	if now.After(leaf.NotAfter) {
		return fmt.Errorf("인증서 만료됨 (만료일: %v)", leaf.NotAfter)
	}

	// 중간 인증서 체인 검증
	if len(cert.Certificate) > 1 {
		pool := x509.NewCertPool()
		for _, der := range cert.Certificate[1:] {
			intermediate, err := x509.ParseCertificate(der)
			if err != nil {
				return fmt.Errorf("중간 인증서 파싱 실패: %w", err)
			}
			pool.AddCert(intermediate)
		}
		opts := x509.VerifyOptions{
			Intermediates: pool,
			CurrentTime:   now,
		}
		if _, err := leaf.Verify(opts); err != nil {
			log.Printf("TLS 경고: 체인 검증 실패 (자체 서명 인증서인 경우 무시 가능): %v", err)
		}
	}

	return nil
}
