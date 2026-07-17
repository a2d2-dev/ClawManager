package services

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const EgressProxyUnreachableCode = "egress_proxy_unreachable"

var isolatedReservedProxyEnvKeys = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"http_proxy",
	"https_proxy",
	"NO_PROXY",
	"no_proxy",
}

type egressProxyPrecheck func(context.Context, string) error

func rejectIsolatedReservedProxyOverrides(overrides map[string]string) error {
	for key := range overrides {
		if canonical, reserved := canonicalIsolatedReservedProxyEnvKey(key); reserved {
			return fmt.Errorf("reserved proxy environment variable %s cannot be overridden for isolated instances", canonical)
		}
	}
	return nil
}

func canonicalIsolatedReservedProxyEnvKey(key string) (string, bool) {
	for _, reserved := range isolatedReservedProxyEnvKeys {
		if strings.EqualFold(strings.TrimSpace(key), reserved) {
			return reserved, true
		}
	}
	return "", false
}

func stripIsolatedReservedProxyEnvKeys(env map[string]string) map[string]string {
	if len(env) == 0 {
		return env
	}
	stripped := make(map[string]string, len(env))
	for key, value := range env {
		if _, reserved := canonicalIsolatedReservedProxyEnvKey(key); reserved {
			continue
		}
		stripped[key] = value
	}
	return stripped
}

func withRequiredProxyEnv(env map[string]string, proxyURL string) map[string]string {
	env = stripIsolatedReservedProxyEnvKeys(env)
	return mergeEnvMaps(env, proxyEnvForURL(proxyURL))
}

func defaultEgressProxyPrecheck(ctx context.Context, rawURL string) error {
	hostPort, err := egressProxyDialAddress(rawURL)
	if err != nil {
		return egressProxyUnreachable(rawURL, err)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(dialCtx, "tcp", hostPort)
		cancel()
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
	}
	return egressProxyUnreachable(rawURL, lastErr)
}

func egressProxyDialAddress(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("proxy URL must include scheme and host")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("proxy URL host is empty")
	}
	port := parsed.Port()
	if port == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", fmt.Errorf("unsupported proxy URL scheme %q", parsed.Scheme)
		}
	}
	return net.JoinHostPort(host, port), nil
}

func egressProxyUnreachable(proxyURL string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%s: proxy %q is unreachable", EgressProxyUnreachableCode, proxyURL)
	}
	return fmt.Errorf("%s: proxy %q is unreachable: %w", EgressProxyUnreachableCode, proxyURL, cause)
}
