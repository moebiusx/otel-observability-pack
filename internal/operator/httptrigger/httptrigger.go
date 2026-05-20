// Package httptrigger implements controller.AzureTrigger by POSTing the
// effective-pack payload to a configured pipeline endpoint. It is the
// production wiring of the "Azure" tool path described in
// docs/operator-design.md §2.1.
//
// The endpoint URL and bearer token live on the AzurePipelineRef-pointed
// secret, not in this package, so credentials never appear in operator
// configuration or logs.
package httptrigger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/example/observability-pack/api/v1alpha1"
)

// Trigger is the production AzureTrigger.
type Trigger struct {
	Client    client.Client
	HTTP      *http.Client
	Namespace string // namespace where credential secrets live (typically Pack.Namespace)
}

// New returns a Trigger with sane defaults.
func New(c client.Client) *Trigger {
	return &Trigger{
		Client: c,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Trigger implements controller.AzureTrigger.
func (t *Trigger) Trigger(ctx context.Context, ref *apiv1.AzurePipelineRef, payload []byte) (*apiv1.PipelineRunInfo, error) {
	url, token, err := t.resolveCredentials(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve credentials: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := t.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pipeline %s returned %d: %s", url, resp.StatusCode, string(body))
	}

	// Accept both Azure DevOps (`id`, `_links.web.href`) and GitHub
	// Actions (`run_id`, `html_url`) response shapes.
	var parsed map[string]any
	_ = json.Unmarshal(body, &parsed)

	info := &apiv1.PipelineRunInfo{
		Status:  "Triggered",
		Started: metav1.NewTime(time.Now()),
	}
	if v, ok := parsed["id"]; ok {
		info.RunID = fmt.Sprint(v)
	}
	if v, ok := parsed["run_id"]; ok && info.RunID == "" {
		info.RunID = fmt.Sprint(v)
	}
	if links, ok := parsed["_links"].(map[string]any); ok {
		if web, ok := links["web"].(map[string]any); ok {
			if href, ok := web["href"].(string); ok {
				info.URL = href
			}
		}
	}
	if v, ok := parsed["html_url"].(string); ok && info.URL == "" {
		info.URL = v
	}
	return info, nil
}

// resolveCredentials looks up the secret named on the AzurePipelineRef.
// The secret schema is:
//
//	url:   the full POST endpoint (azure-devops "runs" URL or GitHub
//	       Actions workflow_dispatch URL)
//	token: bearer token (PAT for Azure DevOps, token for GitHub)
//
// If no secret is configured, we fall back to a pipeline-name-based
// shape that the deployer can preconfigure via env vars later. For the
// MVP, the secret is required.
func (t *Trigger) resolveCredentials(ctx context.Context, ref *apiv1.AzurePipelineRef) (url, token string, err error) {
	if ref == nil || ref.CredentialsSecretRef == "" {
		return "", "", fmt.Errorf("azurePipeline.credentialsSecretRef is required")
	}
	var sec corev1.Secret
	key := types.NamespacedName{Name: ref.CredentialsSecretRef, Namespace: t.Namespace}
	if err := t.Client.Get(ctx, key, &sec); err != nil {
		return "", "", fmt.Errorf("get secret %s: %w", key, err)
	}
	url = string(sec.Data["url"])
	token = string(sec.Data["token"])
	if url == "" {
		return "", "", fmt.Errorf("secret %s missing 'url' key", key)
	}
	return url, token, nil
}
