package packager

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/AlecAivazis/survey/v2"
	"github.com/defenseunicorns/zarf/src/internal/images"
	"github.com/defenseunicorns/zarf/src/internal/message"
	"github.com/defenseunicorns/zarf/src/internal/utils"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/mholt/archiver/v3"
	"github.com/testifysec/witness/pkg/attestation"
	"github.com/testifysec/witness/pkg/attestation/syft"
)

func CatalogImages(tagToImage map[name.Tag]v1.Image, sbomDir, tarPath string) {
	imageCount := len(tagToImage)
	spinner := message.NewProgressSpinner("Creating SBOMs for %d images.", imageCount)
	defer spinner.Stop()

	actx, err := attestation.NewContext([]attestation.Attestor{})
	if err != nil {
		spinner.Fatalf(err, "Unable to make attestation context")
	}

	cachePath := images.CachePath()
	currImage := 1
	for tag := range tagToImage {
		spinner.Updatef("Creating image SBOMs (%d of %d): %s", currImage, imageCount, tag)
		tarballImg, err := tarball.ImageFromPath(tarPath, &tag)
		if err != nil {
			spinner.Fatalf(err, "Unable to open image %s", tag.String())
		}

		sbomAttestor := syft.New(syft.WithImageSource(tarballImg, cachePath, tag.String()))
		if err := sbomAttestor.Attest(actx); err != nil {
			spinner.Fatalf(err, "Unable to build sbom for image %s", tag.String())
		}

		sbomFile, err := os.Create(filepath.Join(sbomDir, fmt.Sprintf("%s.json", sbomAttestor.SBOM.Source.ImageMetadata.ID)))
		if err != nil {
			spinner.Fatalf(err, "Unable to create SBOM file for image %s", tag.String())
		}

		defer sbomFile.Close()
		enc := json.NewEncoder(sbomFile)
		if err := enc.Encode(sbomAttestor); err != nil {
			spinner.Fatalf(err, "Unable to write SBOM file for image %s", tag.String())
		}

		currImage++
	}

	spinner.Success()
}

func PresentSbom(packageName string) {
	tempPath := createPaths()
	defer tempPath.clean()

	if utils.InvalidPath(packageName) {
		message.Fatalf(nil, "The package archive %s seems to be missing or unreadable.", packageName)
	}

	_ = archiver.Extract(packageName, "seed-image.tar", tempPath.base)
	tagsToHash := make(map[string]string)
	if _, err := os.Stat(tempPath.seedImage); err == nil {
		if err := loadImageInfoFromTar(tempPath.seedImage, tagsToHash); err != nil {
			message.Fatal(err, "Unable to read seed images in package.")
		}
	}

	_ = archiver.Extract(packageName, "images.tar", tempPath.base)
	if _, err := os.Stat(tempPath.images); err == nil {
		if err := loadImageInfoFromTar(tempPath.images, tagsToHash); err != nil {
			message.Fatal(err, "Unable to read seed images in package.")
		}
	}

	var selectedTag string
	prompt := &survey.Input{
		Message: "Which image would you like to view the SBOM for?",
		Suggest: func(toComplete string) []string {
			tagStrings := make([]string, 0, 0)
			for tag := range tagsToHash {
				tagStrings = append(tagStrings, tag)
			}

			return tagStrings
		},
	}

	_ = survey.AskOne(prompt, &selectedTag, survey.WithValidator(survey.Required), survey.WithShowCursor(true))
	selectedHash, ok := tagsToHash[selectedTag]
	if !ok {
		message.Fatalf(nil, "Could not find SBOM for image %s", selectedTag)
	}

	sbomFileName := fmt.Sprintf("%s.json", selectedHash)
	_ = archiver.Extract(packageName, "sboms/"+sbomFileName, tempPath.base)
	sbomFile, err := os.Open(tempPath.sboms + "/" + sbomFileName)
	if err != nil {
		message.Fatalf(err, "Could not read SBOM for image %s", selectedTag)
	}

	sbomFileContents, err := io.ReadAll(sbomFile)
	if err != nil {
		message.Fatalf(err, "Could not read SBOM for image %s", selectedTag)
	}

	attestor := syft.Attestor{}
	if err := json.Unmarshal(sbomFileContents, &attestor); err != nil {
		message.Fatalf(err, "Unable to present the SBOM for the image %s", selectedTag)
	}

	output, err := os.Create(sbomFileName)
	if err != nil {
		message.Fatal(err, "Unable to write SBOM")
	}

	defer output.Close()
	spdx, err := attestor.ToSPDX()
	if err != nil {
		message.Fatal(err, "Unable to write SBOM")
	}

	if _, err := output.Write(spdx); err != nil {
		message.Fatal(err, "Unable to write SBOM")
	}
}

func loadImageInfoFromTar(path string, tagsToHash map[string]string) error {
	manifest, err := tarball.LoadManifest(pathOpener(path))
	if err != nil {
		return err
	}

	for _, descriptor := range manifest {
		for _, repoTag := range descriptor.RepoTags {
			tagsToHash[repoTag] = descriptor.Config
		}
	}

	return nil
}

func pathOpener(path string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return os.Open(path)
	}
}
