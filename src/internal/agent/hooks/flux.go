package hooks

import (
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/defenseunicorns/zarf/src/config"
	"github.com/defenseunicorns/zarf/src/internal/agent/operations"
	"github.com/defenseunicorns/zarf/src/internal/git"
	"github.com/defenseunicorns/zarf/src/internal/message"
	"github.com/defenseunicorns/zarf/src/types"
	v1 "k8s.io/api/admission/v1"
)

type SecretRef struct {
	Name string `json:"name"`
}

type GenericGitRepo struct {
	Spec struct {
		URL       string    `json:"url"`
		SecretRef SecretRef `json:"secretRef,omitempty"`
	}
}

// NewGitRepositoryMutationHook creates a new instance of the git repo mutation hook
func NewGitRepositoryMutationHook() operations.Hook {
	message.Debug("hooks.NewGitRepositoryMutationHook()")
	return operations.Hook{
		Create: mutateGitRepository,
		Update: mutateGitRepository,
	}
}

func mutateGitRepository(r *v1.AdmissionRequest) (*operations.Result, error) {
	var patches []operations.PatchOperation

	zarfStatePath := "/etc/zarf-state.json/state"
	stateFile, err := ioutil.ReadFile(zarfStatePath)
	if err != nil {
		message.Warnf("@JPERRY error when trying to read the zarfStateFile: %v", err)
	}

	var zarfState types.ZarfState
	err = json.Unmarshal([]byte(stateFile), &zarfState)
	if err != nil {
		message.Warnf("@JPERRY error when trying to unmarshal the zarfState: %v", err)
	}

	message.Note(fmt.Sprintf("\n\nZarfState = %#v\n\n", zarfState))

	// Default to the InCluster gitURL but check if we initialized with an external server
	gitServerURL := config.ZarfInClusterGitServiceURL
	if !zarfState.GitServerInfo.InternalServer {
		gitServerURL = zarfState.GitServerInfo.GitAddress

		if zarfState.GitServerInfo.GitPort != 0 {
			gitServerURL += fmt.Sprintf(":%d", zarfState.GitServerInfo.GitPort)
		}
	}

	message.Note(fmt.Sprintf("~~~JPERRY~~~ The gitServerURL: %s", gitServerURL))

	// parse to simple struct to read the git url
	gitRepo := &GenericGitRepo{}
	if err := json.Unmarshal(r.Object.Raw, &gitRepo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal manifest: %v", err)
	}

	message.Info(gitRepo.Spec.URL)

	replacedURL := git.MutateGitUrlsInText(gitServerURL, gitRepo.Spec.URL, zarfState.GitServerInfo.GitUsername)

	message.Warnf("@@@JPERRY@@@ The repalcedURL: %s\n", replacedURL)
	patches = append(patches, operations.ReplacePatchOperation("/spec/url", replacedURL))

	// If a prior secret exists, replace it
	if gitRepo.Spec.SecretRef.Name != "" {
		patches = append(patches, operations.ReplacePatchOperation("/spec/secretRef/name", config.ZarfGitServerSecretName))
	} else {
		// Otherwise, add the new secret
		patches = append(patches, operations.AddPatchOperation("/spec/secretRef", SecretRef{Name: config.ZarfGitServerSecretName}))
	}

	return &operations.Result{
		Allowed:  true,
		PatchOps: patches,
	}, nil
}
