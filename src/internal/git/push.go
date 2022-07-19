package git

import (
	"fmt"
	"path/filepath"

	"github.com/defenseunicorns/zarf/src/config"
	"github.com/defenseunicorns/zarf/src/internal/k8s"
	"github.com/defenseunicorns/zarf/src/internal/message"
	"github.com/defenseunicorns/zarf/src/internal/utils"
	"github.com/go-git/go-git/v5"
	goConfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

const offlineRemoteName = "offline-downstream"
const onlineRemoteRefPrefix = "refs/remotes/" + onlineRemoteName + "/"

func PushAllDirectories(localPath string) error {

	gitServerInfo := config.GetGitServerInfo()

	gitServerURL := ""
	if gitServerInfo.InternalServer {
		message.Note("~~jperry we are using the internal git server!")
		// Establish a git tunnel to the internal gitea pod to send the repos
		tunnel := k8s.NewZarfTunnel()
		tunnel.Connect(k8s.ZarfGit, false)
		defer tunnel.Close()

		gitServerURL = fmt.Sprintf("http://%s", tunnel.Endpoint())
	} else {
		gitServerURL = gitServerInfo.GitAddress
		if gitServerInfo.GitPort != 0 {
			gitServerURL += fmt.Sprintf(":%d", gitServerInfo.GitPort)
		}
	}

	message.Warnf("@JPERRY The gitServerURL that we are pushing to is %s", gitServerURL)

	paths, err := utils.ListDirectories(localPath)
	if err != nil {
		message.Warnf("Unable to list the %s directory", localPath)
		return err
	}

	spinner := message.NewProgressSpinner("Processing %d git repos", len(paths))
	defer spinner.Stop()

	for _, path := range paths {
		basename := filepath.Base(path)
		spinner.Updatef("Pushing git repo %s", basename)

		// Open the given repo
		repo, err := git.PlainOpen(path)
		if err != nil {
			return fmt.Errorf("not a valid git repo or unable to open: %w", err)
		}

		// Get the upstream URL
		remote, err := repo.Remote(onlineRemoteName)
		if err != nil {
			return fmt.Errorf("unable to find the git remote: %w", err)
		}

		//TODO: @JPERRY can we do this transorming in the function that calls this one? The name depends on the username provided if external
		remoteUrl := remote.Config().URLs[0]
		targetUrl := transformURL(gitServerURL, remoteUrl, gitServerInfo.GitUsername)

		_, err = repo.CreateRemote(&goConfig.RemoteConfig{
			Name: offlineRemoteName,
			URLs: []string{targetUrl},
		})

		if err != nil && err != git.ErrRemoteExists {
			return fmt.Errorf("failed to create offline remote: %w", err)
		}

		if err := push(*repo, path, gitServerURL, spinner); err != nil {
			spinner.Warnf("Unable to push the git repo %s", basename)
			return err
		}

		// // Add the read-only user to this repo
		// repoPathSplit := strings.Split(path, "/")
		// repoNameWithGitTag := repoPathSplit[len(repoPathSplit)-1]
		// repoName := strings.Split(repoNameWithGitTag, ".git")[0]
		// err = addReadOnlyUserToRepo(gitServerURL, repoName) // TODO: @JPERRY this WILL NOT work with an external git server..
		// if err != nil {
		// message.Warnf("Unable to add the read-only user to the repo: %v\n", repoName)
		// return err
		// }
	}

	spinner.Success()
	return nil
}

func push(repo git.Repository, localPath, tunnelUrl string, spinner *message.Spinner) error {

	// TODO: @JPERRY this is where I should be using the user/pass from the 'Zarf State Secret'
	gitCred := http.BasicAuth{
		// Username: "gitea",
		// Password: "password",
		Username: config.ZarfGitPushUser,
		Password: config.GetSecret(config.StateGitPush),
	}

	// Since we are pushing HEAD:refs/heads/master on deployment, leaving
	// duplicates of the HEAD ref (ex. refs/heads/master,
	// refs/remotes/online-upstream/master, will cause the push to fail)
	removedRefs, err := removeHeadCopies(localPath)
	if err != nil {
		return fmt.Errorf("unable to remove unused git refs from the repo: %w", err)
	}

	err = repo.Push(&git.PushOptions{
		RemoteName: offlineRemoteName,
		Auth:       &gitCred,
		Progress:   spinner,
		// If a provided refspec doesn't push anything, it is just ignored
		RefSpecs: []goConfig.RefSpec{
			"refs/heads/*:refs/heads/*",
			onlineRemoteRefPrefix + "*:refs/heads/*",
			"refs/tags/*:refs/tags/*",
		},
	})

	if err == git.NoErrAlreadyUpToDate {
		spinner.Debugf("Repo already up-to-date")
	} else if err != nil {
		return fmt.Errorf("unable to push repo to the gitops service: %w", err)
	}

	// Add back the refs we removed just incase this push isn't the last thing
	// being run and a later task needs to reference them.
	addRefs(localPath, removedRefs)

	return nil
}
