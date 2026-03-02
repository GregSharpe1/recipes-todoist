package app

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) deleteUploadedFile(imagePath string) error {
	clean := strings.TrimSpace(imagePath)
	if clean == "" {
		return nil
	}
	const prefix = "/uploads/"
	if !strings.HasPrefix(clean, prefix) {
		return nil
	}
	name := strings.TrimSpace(strings.TrimPrefix(clean, prefix))
	if name == "" {
		return nil
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return nil
	}
	return os.Remove(filepath.Join(a.uploadDir, name))
}

func (a *App) saveUploadedFile(file multipart.File, header *multipart.FileHeader) (string, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}

	fileName := fmt.Sprintf("recipe_%d%s", time.Now().UnixNano(), ext)
	absPath := filepath.Join(a.uploadDir, fileName)

	out, err := os.Create(absPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		return "", err
	}

	return "/uploads/" + fileName, nil
}

func (a *App) saveImportedImage(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("unsupported image URL scheme")
	}
	if !isAllowedImportImageHost(u.Hostname()) {
		return "", errors.New("imported image host is not allowed")
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "todoist-recipes/1.0 (+recipe importer)")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("image fetch status %d", resp.StatusCode)
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return "", errors.New("imported URL is not an image")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("empty image response")
	}

	ext := extensionFromImageResponse(contentType, u.Path)
	fileName := fmt.Sprintf("recipe_%d%s", time.Now().UnixNano(), ext)
	absPath := filepath.Join(a.uploadDir, fileName)
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return "", err
	}

	return "/uploads/" + fileName, nil
}

func extensionFromImageResponse(contentType, pathValue string) string {
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	switch strings.TrimSpace(contentType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}

	ext := strings.ToLower(filepath.Ext(pathValue))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	default:
		return ".jpg"
	}
}

func isAllowedImportImageHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "gousto.co.uk", "www.gousto.co.uk", "production-media.gousto.co.uk":
		return true
	case "bbcgoodfood.com", "www.bbcgoodfood.com", "images.immediate.co.uk":
		return true
	default:
		return false
	}
}
