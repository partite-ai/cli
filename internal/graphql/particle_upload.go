package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
)

// uploadParticleQuery is the GraphQL operation hand-executed via multipart.
// genqlient cannot issue this call because it has no Upload support, so the
// operation text is inlined and the response is parsed into the same
// ParticleArtifactDetails type produced by GetParticleArtifact.
const uploadParticleQuery = `mutation UploadParticleArtifact($bytes: Upload!) {
  uploadParticleArtifact(bytes: $bytes) {
    id
    name
    version
    sha256
    description
    capabilities {
      http {
        allowedHosts
      }
    }
    credentials {
      name
      hosts
      required
      methods {
        name
        type
        description
        apiKeyLocation {
          kind
          name
          scheme
        }
        oauth2 {
          authorizationUrl
          tokenUrl
          deviceAuthUrl
          scopes
          flows
        }
        signingKey {
          algorithm
        }
      }
    }
    tools {
      name
      description
      inputSchema
    }
    createdAt
  }
}`

// UploadParticleArtifact POSTs the given body to the GraphQL endpoint as a
// graphql-multipart-request-spec request and returns the parsed artifact.
func UploadParticleArtifact(
	ctx context.Context,
	httpClient *http.Client,
	graphqlURL *url.URL,
	filename string,
	body io.Reader,
) (ParticleArtifactDetails, error) {
	var zero ParticleArtifactDetails

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	operations := map[string]any{
		"query":         uploadParticleQuery,
		"operationName": "UploadParticleArtifact",
		"variables":     map[string]any{"bytes": nil},
	}
	opBytes, err := json.Marshal(operations)
	if err != nil {
		return zero, fmt.Errorf("marshal operations: %w", err)
	}
	if err := mw.WriteField("operations", string(opBytes)); err != nil {
		return zero, fmt.Errorf("write operations field: %w", err)
	}

	mapPart := map[string][]string{"0": {"variables.bytes"}}
	mapBytes, err := json.Marshal(mapPart)
	if err != nil {
		return zero, fmt.Errorf("marshal map: %w", err)
	}
	if err := mw.WriteField("map", string(mapBytes)); err != nil {
		return zero, fmt.Errorf("write map field: %w", err)
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="0"; filename=%q`, filename))
	h.Set("Content-Type", "application/octet-stream")
	filePart, err := mw.CreatePart(h)
	if err != nil {
		return zero, fmt.Errorf("create file part: %w", err)
	}
	if _, err := io.Copy(filePart, body); err != nil {
		return zero, fmt.Errorf("copy file body: %w", err)
	}

	if err := mw.Close(); err != nil {
		return zero, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL.String(), &buf)
	if err != nil {
		return zero, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		return zero, fmt.Errorf("upload returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed struct {
		Data struct {
			UploadParticleArtifact ParticleArtifactDetails `json:"uploadParticleArtifact"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return zero, fmt.Errorf("unmarshal response: %w (body: %s)", err, string(respBytes))
	}
	if len(parsed.Errors) > 0 {
		return zero, fmt.Errorf("graphql error: %s", parsed.Errors[0].Message)
	}
	return parsed.Data.UploadParticleArtifact, nil
}
