package coverage

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"tailscale.com/client/tailscale/v2/tools/internal/openapi"
	"tailscale.com/client/tailscale/v2/tools/internal/repoanalysis"
)

type Report struct {
	SpecPath string
	RepoRoot string
	Endpoint EndpointReport
	Device   DevicePropertyReport
}

type EndpointReport struct {
	TotalSpecOperations        int
	TotalImplementedOperations int
	CoveredOperations          int
	Missing                    []openapi.Operation
	Extra                      []repoanalysis.Operation
	ByTag                      []TagCoverage
}

type TagCoverage struct {
	Tag     string
	Total   int
	Covered int
	Missing int
}

type DevicePropertyReport struct {
	TotalSpecProperties int
	CoveredProperties   int
	Missing             []string
	Extra               []repoanalysis.Field
}

func Build(spec *openapi.Spec, repo *repoanalysis.Analyzer, specPath, repoRoot string) (*Report, error) {
	specOperations, err := spec.Operations()
	if err != nil {
		return nil, err
	}

	repoOperations := repo.Operations()
	specByKey := make(map[string]openapi.Operation, len(specOperations))
	for _, operation := range specOperations {
		specByKey[operationKey(operation.Method, operation.NormalizedPath)] = operation
	}

	repoByKey := make(map[string]repoanalysis.Operation, len(repoOperations))
	for _, operation := range repoOperations {
		key := operationKey(operation.Method, operation.NormalizedPath)
		if _, exists := repoByKey[key]; !exists {
			repoByKey[key] = operation
		}
	}

	endpointReport := EndpointReport{
		TotalSpecOperations:        len(specByKey),
		TotalImplementedOperations: len(repoByKey),
	}

	tagSummary := make(map[string]*TagCoverage)
	for _, operation := range specOperations {
		key := operationKey(operation.Method, operation.NormalizedPath)
		if _, ok := repoByKey[key]; ok {
			endpointReport.CoveredOperations++
		} else {
			endpointReport.Missing = append(endpointReport.Missing, operation)
		}

		tags := operation.Tags
		if len(tags) == 0 {
			tags = []string{"Uncategorized"}
		}

		for _, tag := range tags {
			coverage := tagSummary[tag]
			if coverage == nil {
				coverage = &TagCoverage{Tag: tag}
				tagSummary[tag] = coverage
			}

			coverage.Total++
			if _, ok := repoByKey[key]; ok {
				coverage.Covered++
			} else {
				coverage.Missing++
			}
		}
	}

	for _, operation := range repoByKey {
		if _, ok := specByKey[operationKey(operation.Method, operation.NormalizedPath)]; !ok {
			endpointReport.Extra = append(endpointReport.Extra, operation)
		}
	}

	for _, coverage := range tagSummary {
		endpointReport.ByTag = append(endpointReport.ByTag, *coverage)
	}

	slices.SortFunc(endpointReport.Missing, func(lhs, rhs openapi.Operation) int {
		if strings.Join(lhs.Tags, ",") != strings.Join(rhs.Tags, ",") {
			return strings.Compare(strings.Join(lhs.Tags, ","), strings.Join(rhs.Tags, ","))
		}
		if lhs.Path != rhs.Path {
			return strings.Compare(lhs.Path, rhs.Path)
		}
		return strings.Compare(lhs.Method, rhs.Method)
	})
	slices.SortFunc(endpointReport.Extra, func(lhs, rhs repoanalysis.Operation) int {
		if lhs.Path != rhs.Path {
			return strings.Compare(lhs.Path, rhs.Path)
		}
		return strings.Compare(lhs.Method, rhs.Method)
	})
	slices.SortFunc(endpointReport.ByTag, func(lhs, rhs TagCoverage) int {
		return strings.Compare(lhs.Tag, rhs.Tag)
	})

	specProperties, err := spec.DeviceProperties()
	if err != nil {
		return nil, err
	}

	repoFields, err := repo.StructLeafJSONFields("Device")
	if err != nil {
		return nil, err
	}

	specFieldSet := make(map[string]struct{}, len(specProperties))
	for _, property := range specProperties {
		specFieldSet[property] = struct{}{}
	}

	repoFieldSet := make(map[string]repoanalysis.Field, len(repoFields))
	for _, field := range repoFields {
		repoFieldSet[field.Path] = field
	}

	deviceReport := DevicePropertyReport{
		TotalSpecProperties: len(specFieldSet),
	}

	for _, property := range specProperties {
		if _, ok := repoFieldSet[property]; ok {
			deviceReport.CoveredProperties++
			continue
		}
		deviceReport.Missing = append(deviceReport.Missing, property)
	}

	for _, field := range repoFields {
		if _, ok := specFieldSet[field.Path]; !ok {
			deviceReport.Extra = append(deviceReport.Extra, field)
		}
	}

	slices.Sort(deviceReport.Missing)
	slices.SortFunc(deviceReport.Extra, func(lhs, rhs repoanalysis.Field) int {
		if lhs.Path != rhs.Path {
			return strings.Compare(lhs.Path, rhs.Path)
		}
		if lhs.File != rhs.File {
			return strings.Compare(lhs.File, rhs.File)
		}
		return lhs.Line - rhs.Line
	})

	return &Report{
		SpecPath: specPath,
		RepoRoot: repoRoot,
		Endpoint: endpointReport,
		Device:   deviceReport,
	}, nil
}

func WriteMarkdown(report *Report, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	files := map[string]string{
		"summary.md":                  renderSummary(report),
		"endpoint-coverage.md":        renderEndpointCoverage(report),
		"device-property-coverage.md": renderDeviceCoverage(report),
	}

	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(outputDir, name), []byte(contents), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return nil
}

func renderSummary(report *Report) string {
	var builder strings.Builder
	builder.WriteString("# Coverage Gap Summary\n\n")
	builder.WriteString("| Area | OpenAPI | Implemented | Missing |\n")
	builder.WriteString("| --- | ---: | ---: | ---: |\n")
	builder.WriteString(fmt.Sprintf("| Endpoint operations | %d | %d | %d |\n",
		report.Endpoint.TotalSpecOperations,
		report.Endpoint.CoveredOperations,
		len(report.Endpoint.Missing),
	))
	builder.WriteString(fmt.Sprintf("| Device properties | %d | %d | %d |\n",
		report.Device.TotalSpecProperties,
		report.Device.CoveredProperties,
		len(report.Device.Missing),
	))
	builder.WriteString("\n")
	builder.WriteString(fmt.Sprintf("- Spec: `%s`\n", report.SpecPath))
	builder.WriteString(fmt.Sprintf("- Repo: `%s`\n", report.RepoRoot))
	builder.WriteString("- Reports:\n")
	builder.WriteString("  - `endpoint-coverage.md`\n")
	builder.WriteString("  - `device-property-coverage.md`\n")
	return builder.String()
}

func renderEndpointCoverage(report *Report) string {
	var builder strings.Builder
	builder.WriteString("# Endpoint Coverage\n\n")
	builder.WriteString("| Tag | OpenAPI | Covered | Missing |\n")
	builder.WriteString("| --- | ---: | ---: | ---: |\n")
	for _, coverage := range report.Endpoint.ByTag {
		builder.WriteString(fmt.Sprintf("| %s | %d | %d | %d |\n", coverage.Tag, coverage.Total, coverage.Covered, coverage.Missing))
	}

	builder.WriteString("\n## Missing From The Client\n\n")
	if len(report.Endpoint.Missing) == 0 {
		builder.WriteString("No missing OpenAPI operations were found.\n")
	} else {
		builder.WriteString("| Method | Path | Operation ID | Tags | Summary |\n")
		builder.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, operation := range report.Endpoint.Missing {
			builder.WriteString(fmt.Sprintf("| %s | `%s` | `%s` | %s | %s |\n",
				operation.Method,
				operation.Path,
				operation.OperationID,
				strings.Join(operation.Tags, ", "),
				operation.Summary,
			))
		}
	}

	builder.WriteString("\n## Implemented But Missing From OpenAPI\n\n")
	if len(report.Endpoint.Extra) == 0 {
		builder.WriteString("All implemented client operations matched an OpenAPI path.\n")
	} else {
		builder.WriteString("| Method | Path | Client Method | Source |\n")
		builder.WriteString("| --- | --- | --- | --- |\n")
		for _, operation := range report.Endpoint.Extra {
			builder.WriteString(fmt.Sprintf("| %s | `%s` | `%s` | `%s:%d` |\n",
				operation.Method,
				operation.Path,
				operation.ClientMethod,
				repoanalysis.RelativePath(report.RepoRoot, operation.File),
				operation.Line,
			))
		}
	}

	return builder.String()
}

func renderDeviceCoverage(report *Report) string {
	var builder strings.Builder
	builder.WriteString("# Device Property Coverage\n\n")
	builder.WriteString(fmt.Sprintf("- OpenAPI device properties: `%d`\n", report.Device.TotalSpecProperties))
	builder.WriteString(fmt.Sprintf("- Covered in `Device`: `%d`\n", report.Device.CoveredProperties))
	builder.WriteString(fmt.Sprintf("- Missing from `Device`: `%d`\n", len(report.Device.Missing)))
	builder.WriteString(fmt.Sprintf("- Present in `Device` but not OpenAPI: `%d`\n\n", len(report.Device.Extra)))

	builder.WriteString("## Missing From `Device`\n\n")
	if len(report.Device.Missing) == 0 {
		builder.WriteString("No missing device properties were found.\n")
	} else {
		builder.WriteString("| Property |\n")
		builder.WriteString("| --- |\n")
		for _, property := range report.Device.Missing {
			builder.WriteString(fmt.Sprintf("| `%s` |\n", property))
		}
	}

	builder.WriteString("\n## Present In `Device` But Not OpenAPI\n\n")
	if len(report.Device.Extra) == 0 {
		builder.WriteString("No extra device properties were found.\n")
	} else {
		builder.WriteString("| Property | Source |\n")
		builder.WriteString("| --- | --- |\n")
		for _, field := range report.Device.Extra {
			builder.WriteString(fmt.Sprintf("| `%s` | `%s:%d` |\n",
				field.Path,
				repoanalysis.RelativePath(report.RepoRoot, field.File),
				field.Line,
			))
		}
	}

	return builder.String()
}

func operationKey(method, path string) string {
	return method + " " + path
}
