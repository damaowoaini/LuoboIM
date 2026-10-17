// Package iptrust 决定一个请求的真实客户端 IP。
//
// 规则：
//  1. 连接对端（TCP peer）不是可信代理时，直接用对端 IP，任何转发头和请求体里的 ip 都不认。
//  2. 对端是可信代理（nginx 等）时，先认 proxyHeader（如配置），再从 X-Forwarded-For 右往左
//     找第一个不是可信代理的地址，作为「边缘 IP」。
//  3. 边缘 IP 是可信上游（路由服务等会代报真实用户 IP 的服务）时，才认请求体里的 ip，
//     其次 X-Real-IP；否则边缘 IP 就是客户端 IP。
//
// 这样客户端自己填的 ip、伪造的 X-Real-IP / X-Forwarded-For 都不会生效。
package iptrust

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// DefaultProxies 未配置 trustedProxies 时视为可信代理的网段：回环、内网、容器网段。
var DefaultProxies = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"::1/128", "fc00::/7",
}

type Resolver struct {
	proxies     []*net.IPNet
	forwarders  []*net.IPNet
	proxyHeader string
}

// New proxies 为空时使用 DefaultProxies；forwarders 为空表示没有任何上游可以代报客户端 IP。
func New(proxies, forwarders []string, proxyHeader string) (*Resolver, error) {
	if len(proxies) == 0 {
		proxies = DefaultProxies
	}
	p, err := parseNets(proxies)
	if err != nil {
		return nil, fmt.Errorf("ipTrust.trustedProxies: %w", err)
	}
	f, err := parseNets(forwarders)
	if err != nil {
		return nil, fmt.Errorf("ipTrust.trustedForwarders: %w", err)
	}
	proxyHeader = strings.TrimSpace(proxyHeader)
	if strings.EqualFold(proxyHeader, "X-Forwarded-For") {
		proxyHeader = "" // XFF 已按右往左规则处理，不当单值头用
	}
	return &Resolver{proxies: p, forwarders: f, proxyHeader: proxyHeader}, nil
}

func parseNets(items []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(items))
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			ip := net.ParseIP(s)
			if ip == nil {
				return nil, fmt.Errorf("无效的 IP: %q", s)
			}
			if ip.To4() != nil {
				s += "/32"
			} else {
				s += "/128"
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("无效的网段: %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func contains(nets []*net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func parseIP(s string) net.IP {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return net.ParseIP(s)
}

// ClientIP remoteAddr 为 http.Request.RemoteAddr；claimed 为请求体里上游代报的 ip（没有就传空）。
func (r *Resolver) ClientIP(remoteAddr string, h http.Header, claimed string) string {
	host := remoteAddr
	if hp, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = hp
	}
	peer := parseIP(host)
	if peer == nil {
		return host
	}

	edge := peer
	if contains(r.proxies, peer) {
		edge = r.edgeBehindProxy(peer, h)
	}

	if contains(r.forwarders, edge) {
		if ip := parseIP(claimed); ip != nil {
			return ip.String()
		}
		if ip := parseIP(h.Get("X-Real-IP")); ip != nil {
			return ip.String()
		}
	}
	return edge.String()
}

func (r *Resolver) edgeBehindProxy(peer net.IP, h http.Header) net.IP {
	if r.proxyHeader != "" {
		if ip := parseIP(h.Get(r.proxyHeader)); ip != nil {
			return ip
		}
	}
	parts := strings.Split(strings.Join(h.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := parseIP(parts[i])
		if ip == nil {
			continue
		}
		if !contains(r.proxies, ip) {
			return ip
		}
	}
	return peer
}
