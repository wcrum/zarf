package packager

import (
	"strings"

	"github.com/defenseunicorns/zarf/cli/config"
	"github.com/defenseunicorns/zarf/cli/internal/message"
	"github.com/defenseunicorns/zarf/cli/internal/packager/validate"
	"github.com/defenseunicorns/zarf/cli/internal/utils"
	"github.com/defenseunicorns/zarf/cli/types"
)

func GetComposedComponents(tempPaths tempPaths) (components []types.ZarfComponent) {
	for _, component := range config.GetComponents() {
		// Check for standard component.
		if !hasComposedPackage(&component) {
			// Append standard component to list.
			components = append(components, component)
		} else if shouldComposePackage(&component) { // Validate and confirm inclusion of imported package.
			// Expand and add components from imported package.
			importedComponents := getSubPackageAssets(component, tempPaths)
			components = append(components, importedComponents...)
		}
	}
	// Update the parent package config with the expanded sub components.
	// This is important when the deploy package is created.
	config.SetComponents(components)
	return components
}

// Returns true if import field is populated.
func hasComposedPackage(component *types.ZarfComponent) bool {
	return component.Import != types.ZarfImport{}
}

// Validates and confirms inclusion of imported package.
func shouldComposePackage(component *types.ZarfComponent) bool {
	return canComposePackage(component) && (config.DeployOptions.Confirm || component.Required || ConfirmOptionalComponent(*component))
}

// Validates the sub component, errors out if validation fails.
func canComposePackage(component *types.ZarfComponent) bool {
	if err := validate.ValidateImportPackage(component); err != nil {
		message.Fatalf(err, "Invalid import definition in the %s component: %s", component.Name, err)
	}
	return true
}

// Get expanded components from imported component.
func getSubPackageAssets(importComponent types.ZarfComponent, tempPaths tempPaths) (components []types.ZarfComponent) {
	// Read the imported package.
	importedPackage := getSubPackage(&importComponent, tempPaths)
	// Iterate imported components.
	for _, componentToCompose := range importedPackage.Components {
		// Check for standard component.
		if !hasComposedPackage(&componentToCompose) {
			// Doctor standard component name and included files.
			prepComponentToCompose(&componentToCompose, importedPackage.Metadata.Name, importComponent.Import.Path, tempPaths)
			components = append(components, componentToCompose)
		} else if shouldComposePackage(&componentToCompose) {
			// Recurse on imported components.
			components = append(components, getSubPackageAssets(componentToCompose, tempPaths)...)
		}
	}
	return components
}

// Reads the locally imported zarf.yaml
func getSubPackage(component *types.ZarfComponent, tempPath tempPaths) (importedPackage types.ZarfPackage) {
	componentPath := component.Import.Path + "zarf.yaml"
	// Import.Path is a remote package file.
	if utils.IsUrl(component.Import.Path) {
		// Download to temp folder before reading the packages yaml.
		componentPath = downloadRemoteFile(component.Import.Path, component.Name, "zarf.yaml", tempPath)
	}
	utils.ReadYaml(componentPath, &importedPackage)
	return importedPackage
}

// Downloads remote file to temp folder with structure of original package.
func downloadRemoteFile(componentPath string, importComponentName string, filePath string, tempPaths tempPaths) (destinationFile string) {
	importPath := tempPaths.imports
	// Create the temp file path for imported component.
	importFilePath := importComponentName + "/" + filePath
	// Create slice of directories in the imported files path.
	directories := strings.Split(importFilePath, "/")
	// Retrieve the file name to copy downloaded contents to.
	fileName := directories[len(directories)-1]
	// Ensure all directories are created and added to the import path.
	for _, folder := range directories[0 : len(directories)-1] {
		importPath = importPath + "/" + folder
		_ = utils.CreateDirectory(importPath, 0700)
	}
	// Create the destination file path.
	destinationFile = importPath + "/" + fileName
	// Download to the created file.
	utils.DownloadToFile(componentPath+filePath, destinationFile)

	return destinationFile
}

// Updates the name and sets all local asset paths relative to the importing package.
func prepComponentToCompose(component *types.ZarfComponent, parentPackageName string, importPath string, tempPaths tempPaths) {
	// Prefix component name with parent package name to distinguish similarly named components.
	component.Name = parentPackageName + "-" + component.Name

	// Prefix composed component file paths.
	for fileIdx, file := range component.Files {
		composedPath := getComposedFilePath(file.Source, importPath)
		if composedPath != file.Source {
			if utils.IsUrl(composedPath) {
				composedPath = downloadRemoteFile(importPath, parentPackageName, file.Source, tempPaths)
			}
			component.Files[fileIdx].Source = composedPath
		}
	}

	// Prefix non-url composed component chart values files.
	for chartIdx, chart := range component.Charts {
		for valuesIdx, valuesFile := range chart.ValuesFiles {
			composedPath := getComposedFilePath(valuesFile, importPath)
			if composedPath != valuesFile {
				if utils.IsUrl(composedPath) {
					composedPath = downloadRemoteFile(importPath, parentPackageName, valuesFile, tempPaths)
				}
				component.Charts[chartIdx].ValuesFiles[valuesIdx] = composedPath
			}
		}
	}

	// Prefix non-url composed manifest files and kustomizations.
	for manifestIdx, manifest := range component.Manifests {
		for fileIdx, file := range manifest.Files {
			composedPath := getComposedFilePath(file, importPath)
			if composedPath != file {
				if utils.IsUrl(composedPath) {
					composedPath = downloadRemoteFile(importPath, parentPackageName, file, tempPaths)
				}
				component.Manifests[manifestIdx].Files[fileIdx] = composedPath
			}
		}
		for kustomIdx, kustomization := range manifest.Kustomizations {
			composedPath := getComposedFilePath(kustomization, importPath)
			if composedPath != kustomization {
				if utils.IsUrl(composedPath) {
					composedPath = downloadRemoteFile(importPath, parentPackageName, kustomization, tempPaths)
				}
				component.Manifests[manifestIdx].Kustomizations[kustomIdx] = composedPath
			}
		}
	}
}

// Prefix file path with importPath if original file path is not a url.
func getComposedFilePath(originalPath string, pathPrefix string) (composedFilePath string) {
	composedFilePath = originalPath
	// Only prefix if the path is not a url.
	if !utils.IsUrl(composedFilePath) {
		composedFilePath = pathPrefix + composedFilePath
	}
	return composedFilePath
}
