package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

func (a *App) PushToTodoist(ctx context.Context, recipeID string, selectedIndexes []int, enforceSelection bool) error {
	token := strings.TrimSpace(os.Getenv("TODOIST_API_TOKEN"))
	if token == "" {
		return errors.New("TODOIST_API_TOKEN is not set")
	}

	recipe, err := a.getRecipeByID(ctx, recipeID)
	if err != nil {
		return err
	}
	return a.pushNamedItems(token, recipe.Name, recipe.Ingredients, selectedIndexes, enforceSelection)
}

func (a *App) PushRegularListToTodoist(ctx context.Context, listID string, selectedIndexes []int, enforceSelection bool) error {
	token := strings.TrimSpace(os.Getenv("TODOIST_API_TOKEN"))
	if token == "" {
		return errors.New("TODOIST_API_TOKEN is not set")
	}

	list, err := a.getRegularListByID(ctx, listID)
	if err != nil {
		return err
	}
	return a.pushNamedItems(token, list.Name, list.Items, selectedIndexes, enforceSelection)
}

func (a *App) pushNamedItems(token, name string, items []string, selectedIndexes []int, enforceSelection bool) error {
	if len(items) == 0 {
		return errors.New("list has no items")
	}

	pushable := items
	if enforceSelection {
		if len(selectedIndexes) == 0 {
			return errors.New("select at least one ingredient")
		}
		selectedSet := make(map[int]struct{}, len(selectedIndexes))
		for _, idx := range selectedIndexes {
			if idx >= 0 && idx < len(items) {
				selectedSet[idx] = struct{}{}
			}
		}
		filtered := make([]string, 0, len(selectedSet))
		for idx, ingredient := range items {
			if _, ok := selectedSet[idx]; !ok {
				continue
			}
			text := strings.TrimSpace(ingredient)
			if text == "" {
				continue
			}
			filtered = append(filtered, text)
		}
		pushable = filtered
	}
	if len(pushable) == 0 {
		return errors.New("select at least one ingredient")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(pushable))

	for _, ingredient := range pushable {
		ingredient := ingredient
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.pushIngredient(token, name, ingredient); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	errorsFound := make([]string, 0)
	seen := map[string]struct{}{}
	for err := range errCh {
		msg := err.Error()
		if _, ok := seen[msg]; ok {
			continue
		}
		seen[msg] = struct{}{}
		errorsFound = append(errorsFound, msg)
	}
	if len(errorsFound) > 0 {
		return fmt.Errorf("todoist push failed: %s", strings.Join(errorsFound, "; "))
	}

	return nil
}

func (a *App) pushIngredient(token, recipeName, ingredient string) error {
	payload := map[string]string{
		"content": fmt.Sprintf("%s (%s)", ingredient, recipeName),
	}
	if a.projectID != "" {
		payload["project_id"] = a.projectID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(a.apiBaseURL, "/")+"/tasks", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("todoist status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	return nil
}

func (a *App) validateTodoistProject() error {
	token := strings.TrimSpace(os.Getenv("TODOIST_API_TOKEN"))
	if token == "" {
		return errors.New("TODOIST_API_TOKEN is not set")
	}
	if strings.TrimSpace(a.projectID) == "" {
		return errors.New("project ID is empty")
	}

	url := fmt.Sprintf("%s/projects/%s", strings.TrimRight(a.apiBaseURL, "/"), a.projectID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("status %d from %s: %s", resp.StatusCode, url, strings.TrimSpace(string(body)))
}
