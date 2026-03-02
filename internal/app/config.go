package app

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func (a *App) externalBaseURL(r *http.Request) string {
	if a.baseURL != "" {
		return a.baseURL
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost:8080"
	}
	return "http://" + host
}

func resolveBaseURL(flagBaseURL, flagLocalIP, listenAddr string) string {
	if v := strings.TrimSpace(flagBaseURL); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	if v := strings.TrimSpace(flagLocalIP); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	if v := strings.TrimSpace(os.Getenv("BASE_URL")); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	if v := strings.TrimSpace(os.Getenv("LOCAL_IP")); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	return ""
}

func normalizeBaseURL(raw, listenAddr string) string {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	if strings.Contains(v, ":") {
		return "http://" + v
	}
	port := "8080"
	if strings.HasPrefix(listenAddr, ":") && len(listenAddr) > 1 {
		port = listenAddr[1:]
	}
	return fmt.Sprintf("http://%s:%s", v, port)
}

func resolveTodoistProjectID(flagProjectID string) string {
	if v := strings.TrimSpace(flagProjectID); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TODOIST_PROJECT_ID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TODOIST_PROJECT")); v != "" {
		return v
	}
	return ""
}

func resolveTodoistAPIBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("TODOIST_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.todoist.com/api/v1"
}

func (a *App) redirectError(w http.ResponseWriter, r *http.Request, msg string) {
	target := "/?error=" + urlQueryEscape(msg)
	if tab := currentTab(r); tab == "regular" {
		target += "&tab=regular"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (a *App) redirectNotice(w http.ResponseWriter, r *http.Request, msg string) {
	target := "/?notice=" + urlQueryEscape(msg)
	if tab := currentTab(r); tab == "regular" {
		target += "&tab=regular"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}
