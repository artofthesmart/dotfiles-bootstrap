package copilot

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	MinimumVersion            = "1.0.100"
	RequirementEvidenceDate   = "2026-10-20"
	GitHubDocsBYOKURL         = "https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models"
	GitHubDocsInstallURL      = "https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli"
	OllamaIntegrationDocsURL  = "https://docs.ollama.com/integrations/copilot-cli"
	OfficialReleaseRepository = "github/copilot-cli"
)

var versionPattern = regexp.MustCompile(`(?i)(?:^|[^0-9])v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][0-9A-Za-z.-]+)?(?:$|[^0-9])`)

type Version struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

func ParseVersion(output string) (Version, error) {
	value := strings.TrimSpace(output)
	match := versionPattern.FindStringSubmatch(value)
	if len(match) != 4 {
		return Version{}, fmt.Errorf("invalid Copilot CLI version output")
	}
	parts := [3]int{}
	for index := range parts {
		parsed, err := strconv.Atoi(match[index+1])
		if err != nil || parsed < 0 {
			return Version{}, fmt.Errorf("invalid Copilot CLI version output")
		}
		parts[index] = parsed
	}
	return Version{Major: parts[0], Minor: parts[1], Patch: parts[2], Raw: fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2])}, nil
}

func (version Version) AtLeast(required Version) bool {
	if version.Major != required.Major {
		return version.Major > required.Major
	}
	if version.Minor != required.Minor {
		return version.Minor > required.Minor
	}
	return version.Patch >= required.Patch
}

func minimumVersion() Version {
	version, err := ParseVersion(MinimumVersion)
	if err != nil {
		panic(err)
	}
	return version
}
