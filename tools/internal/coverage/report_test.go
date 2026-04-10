package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tailscale.com/client/tailscale/v2/tools/internal/openapi"
	"tailscale.com/client/tailscale/v2/tools/internal/repoanalysis"
)

func TestBuildAndWriteMarkdown(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	specPath := filepath.Join(t.TempDir(), "spec.yaml")
	outputDir := filepath.Join(t.TempDir(), "coverage")

	const repoSource = `package tailscale

import (
	"context"
	"net/http"
	"net/url"
)

type Client struct{}

func (c *Client) buildRequest(ctx context.Context, method string, uri *url.URL, opts ...any) (*http.Request, error) {
	return nil, nil
}

func (c *Client) buildURL(pathElements ...any) *url.URL { return nil }
func (c *Client) buildTailnetURL(pathElements ...any) *url.URL { return nil }

type DevicesResource struct{ *Client }

type DERPRegion struct {
	Preferred           bool    ` + "`json:\"preferred,omitempty\"`" + `
	LatencyMilliseconds float64 ` + "`json:\"latencyMs\"`" + `
}

type ClientConnectivity struct {
	Latency map[string]DERPRegion ` + "`json:\"latency\"`" + `
}

type DevicePostureIdentity struct {
	SerialNumbers []string ` + "`json:\"serialNumbers\"`" + `
}

type Device struct {
	ID                 string                 ` + "`json:\"id\"`" + `
	AdvertisedRoutes   []string               ` + "`json:\"AdvertisedRoutes\"`" + `
	ClientConnectivity *ClientConnectivity    ` + "`json:\"clientConnectivity\"`" + `
	PostureIdentity    *DevicePostureIdentity ` + "`json:\"postureIdentity\"`" + `
}

func (dr *DevicesResource) Get(ctx context.Context, deviceID string) error {
	_, _ = dr.buildRequest(ctx, http.MethodGet, dr.buildURL("device", deviceID))
	return nil
}

func (dr *DevicesResource) List(ctx context.Context) error {
	_, _ = dr.buildRequest(ctx, http.MethodGet, dr.buildTailnetURL("devices"))
	return nil
}
`

	const spec = `openapi: 3.1.0
paths:
  /tailnet/{tailnet}/devices:
    get:
      operationId: listTailnetDevices
      summary: List tailnet devices
      tags:
        - Devices
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  devices:
                    type: array
                    items:
                      $ref: '#/components/schemas/Device'
  /device/{deviceId}:
    get:
      operationId: getDevice
      summary: Get a device
      tags:
        - Devices
      responses:
        '200':
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Device'
  /device/{deviceId}/expire:
    post:
      operationId: expireDeviceKey
      summary: Expire a device key
      tags:
        - Devices
      responses:
        '200':
          description: OK
components:
  schemas:
    Device:
      type: object
      properties:
        id:
          type: string
        advertisedRoutes:
          type: array
          items:
            type: string
        multipleConnections:
          type: boolean
        postureIdentity:
          type: object
          properties:
            serialNumbers:
              type: array
              items:
                type: string
            hardwareAddresses:
              type: array
              items:
                type: string
        clientConnectivity:
          type: object
          properties:
            latency:
              type: object
              additionalProperties:
                type: object
                properties:
                  preferred:
                    type: boolean
                  latencyMs:
                    type: number
`

	if err := os.WriteFile(filepath.Join(repoRoot, "sample.go"), []byte(repoSource), 0o644); err != nil {
		t.Fatalf("WriteFile(sample.go): %v", err)
	}
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("WriteFile(spec): %v", err)
	}

	loadedSpec, err := openapi.Load(specPath)
	if err != nil {
		t.Fatalf("Load(spec): %v", err)
	}

	repo, err := repoanalysis.Analyze(repoRoot)
	if err != nil {
		t.Fatalf("Analyze(repo): %v", err)
	}

	report, err := Build(loadedSpec, repo, specPath, repoRoot)
	if err != nil {
		t.Fatalf("Build(report): %v", err)
	}

	if len(report.Endpoint.Missing) != 1 || report.Endpoint.Missing[0].OperationID != "expireDeviceKey" {
		t.Fatalf("missing operations = %#v, want expireDeviceKey", report.Endpoint.Missing)
	}

	missingProperties := strings.Join(report.Device.Missing, ",")
	for _, want := range []string{
		"advertisedRoutes",
		"multipleConnections",
		"postureIdentity.hardwareAddresses",
	} {
		if !strings.Contains(missingProperties, want) {
			t.Fatalf("missing properties %q did not contain %q", missingProperties, want)
		}
	}

	if len(report.Device.Extra) != 1 || report.Device.Extra[0].Path != "AdvertisedRoutes" {
		t.Fatalf("extra properties = %#v, want AdvertisedRoutes", report.Device.Extra)
	}

	if err := WriteMarkdown(report, outputDir); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	summary, err := os.ReadFile(filepath.Join(outputDir, "summary.md"))
	if err != nil {
		t.Fatalf("ReadFile(summary.md): %v", err)
	}
	if !strings.Contains(string(summary), "Endpoint operations") {
		t.Fatalf("summary.md did not contain endpoint summary: %s", string(summary))
	}

	endpoints, err := os.ReadFile(filepath.Join(outputDir, "endpoint-coverage.md"))
	if err != nil {
		t.Fatalf("ReadFile(endpoint-coverage.md): %v", err)
	}
	if !strings.Contains(string(endpoints), "expireDeviceKey") {
		t.Fatalf("endpoint-coverage.md did not mention expireDeviceKey: %s", string(endpoints))
	}

	deviceCoverage, err := os.ReadFile(filepath.Join(outputDir, "device-property-coverage.md"))
	if err != nil {
		t.Fatalf("ReadFile(device-property-coverage.md): %v", err)
	}
	if !strings.Contains(string(deviceCoverage), "postureIdentity.hardwareAddresses") {
		t.Fatalf("device-property-coverage.md did not mention missing posture field: %s", string(deviceCoverage))
	}
}
