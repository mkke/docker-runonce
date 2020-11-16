package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	docker_cli "docker.io/go-docker"
	docker_t "docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"docker.io/go-docker/api/types/filters"
	"docker.io/go-docker/api/types/mount"
	"docker.io/go-docker/api/types/network"
	"github.com/dustin/go-humanize"
	"github.com/gofrs/flock"
	"github.com/mkke/go-docker/attach"
	"github.com/mkke/go-docker/responses"
	"github.com/mkke/go-mlog"
	"github.com/mkke/go-signalerror"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

var (
	buildDate = "<unknown>"
	buildID   = "<wip>"
)

var (
	timeout             string
	bindCwd             string
	memoryLimit         string
	optionLabelPrefix   string
	imageName           string
	verbose             bool
	stopTimeout         int
	concurrentExecution bool
	forwardImageArgs    bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:           "docker-runonce",
	Short:         "run docker image once",
	RunE:          run,
	SilenceErrors: true,
	SilenceUsage:  true,
}

var log = mlog.NewWriterLogger(os.Stderr)

func run(cmd *cobra.Command, args []string) error {
	if imageName == "" {
		return errors.New("image-name not specified")
	}
	if !strings.Contains(imageName, ":") {
		imageName += ":latest"
	}

	optionRegexp, err := regexp.Compile("^" + regexp.QuoteMeta(optionLabelPrefix) + "(.+)$")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	go func() {
		for {
			select {
			case sig := <-signalCh:
				fmt.Printf("received signal %s\n", sig)
				cancel()
			}
		}
	}()

	dlog := mlog.WithPrefix("Docker", log)
	if verbose {
		dlog.Println("connecting to docker engine...")
	}
	docker, err := docker_cli.NewEnvClient()
	if err != nil {
		return err
	}
	defer docker.Close()

	ping, err := docker.Ping(context.Background())
	if err != nil {
		return err
	}
	if verbose {
		dlog.Printf("connected, api version = %s", ping.APIVersion)
	}

	if strings.Contains(imageName, "/") {
		if verbose {
			dlog.Printf("pulling %s", imageName)
		}
		resp, err := docker.ImagePull(ctx, imageName, docker_t.ImagePullOptions{})
		if err != nil {
			return err
		}
		if err = responses.ParseStreamBody(resp, dlog); err != nil {
			return err
		}
	}

	filters := filters.NewArgs()
	filters.Add("reference", imageName)
	imageSummaries, err := docker.ImageList(ctx, docker_t.ImageListOptions{
		Filters: filters,
	})
	if err != nil {
		return err
	}

	if len(imageSummaries) != 1 {
		return errors.Errorf("could not locate image '%s'", imageName)
	}
	imageSummary := imageSummaries[0]

	for label, value := range imageSummary.Labels {
		if m := optionRegexp.FindStringSubmatch(label); m != nil {
			switch m[1] {
			case "MEMORY_LIMIT":
				memoryLimit = value
			case "BIND_CWD":
				bindCwd = value
			case "TIMEOUT":
				timeout = value
			case "CONCURRENT":
				concurrentExecution = value == "true"
			}
		}
	}

	memoryLimitBytes, err := humanize.ParseBytes(memoryLimit)
	if err != nil {
		return errors.Wrapf(err, "invalid memory limit '%s'", memoryLimit)
	}

	runTimeout, err := time.ParseDuration(timeout)
	if err != nil {
		return errors.Wrapf(err, "invalid run timeout '%s'", timeout)
	}

	if verbose {
		log.Printf("run timeout = %s, memory limit = %s, concurrent execution = %t\n",
			runTimeout.String(), humanize.IBytes(memoryLimitBytes), concurrentExecution)
	}

	if !concurrentExecution {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}

		lock := flock.New(exePath)
		locked, err := lock.TryLock()
		if err != nil {
			return err
		}

		if !locked {
			return errors.New("another instance is already running")
		}
		defer lock.Unlock()
	}

	ctx, _ = context.WithTimeout(ctx, runTimeout)

	oomKillDisable := false

	volumes := make(map[string]struct{})
	var binds []string
	var mounts []mount.Mount

	if bindCwd != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}

		mounts = append(mounts, mount.Mount{
			Type:     "bind",
			Source:   cwd,
			Target:   bindCwd,
			ReadOnly: false,
		})
	}

	resp, err := docker.ContainerCreate(ctx, &container.Config{
		AttachStdin:     true,
		AttachStdout:    true,
		AttachStderr:    true,
		Tty:             false,
		OpenStdin:       true,
		StdinOnce:       true,
		Cmd:             args,
		Image:           imageName,
		Volumes:         volumes,
		NetworkDisabled: false,
		StopTimeout:     &stopTimeout,
	}, &container.HostConfig{
		Binds:          binds,
		NetworkMode:    "host",
		RestartPolicy:  container.RestartPolicy{Name: "no"},
		AutoRemove:     true,
		VolumeDriver:   "local",
		OomScoreAdj:    1000,
		Privileged:     false,
		ReadonlyRootfs: false,
		Resources: container.Resources{
			Memory:            int64(memoryLimitBytes),
			MemoryReservation: int64(memoryLimitBytes),
			OomKillDisable:    &oomKillDisable,
			PidsLimit:         128,
		},
		Mounts: mounts,
	}, &network.NetworkingConfig{}, "")
	if err != nil {
		return err
	}

	containerId := resp.ID
	defer cleanupContainer(docker, containerId)

	for _, w := range resp.Warnings {
		dlog.Println(w)
	}

	if verbose {
		dlog.Printf("container id = %s\n", resp.ID)
	}

	if err := docker.ContainerStart(ctx, containerId, docker_t.ContainerStartOptions{}); err != nil {
		return err
	}

	hr, err := docker.ContainerAttach(ctx, containerId, docker_t.ContainerAttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Logs:   true,
	})
	if err != nil {
		return err
	}
	defer hr.Close()

	ah := attach.NewHandler(hr).WithStdout(os.Stdout).WithStderr(os.Stderr).WithStdin(os.Stdin)
	defer ah.Close()

	attachClosedCh := make(chan struct{})
	ah.AddCloseListener(attachClosedCh)
	ah.Start()

	select {
	case <-time.After(runTimeout):
		cancel()
	case <-attachClosedCh:
		cancel()
	case <-ctx.Done():
	}
	return nil
}

func cleanupContainer(docker *docker_cli.Client, containerId string) {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	_ = docker.ContainerRemove(ctx, containerId, docker_t.ContainerRemoveOptions{
		Force: true,
	})
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func main() {
	if forwardImageArgs {
		rootCmd.SetArgs(append([]string{"--"}, os.Args[1:]...))
	}

	if err := rootCmd.Execute(); err != nil {
		if verbose {
			log.Printf("Process ends abnormally. Reason: %v\n", err)
		} else {
			log.Println(err)
		}
		if code, ok := signalerror.ErrSignalExitCode(err); ok {
			os.Exit(code)
		} else {
			os.Exit(1)
		}
	} else {
		if verbose {
			log.Printf("Process ends normally.\n")
		}
		os.Exit(0)
	}
}

func init() {
	if exeName := filepath.Base(os.Args[0]); exeName != "" && exeName != "docker-runonce" {
		imageName = exeName
		forwardImageArgs = true
	}

	rootCmd.Version = "1 (commit " + buildID + " built on " + buildDate + ")"
	rootCmd.Flags().StringVar(&timeout, "timeout", "10s", "give up retrying after this time")
	rootCmd.Flags().IntVar(&stopTimeout, "stop-timeout", 1, "stop timeout in seconds")
	rootCmd.Flags().StringVar(&bindCwd, "bind-cwd", "/host", "target path to bind-mount current working directory")
	rootCmd.Flags().StringVar(&memoryLimit, "memory-limit", "128Mi", "container memory limit")
	rootCmd.Flags().StringVar(&optionLabelPrefix, "option-label-prefix", "DRO_", "prefix for image labels to use as options")
	rootCmd.Flags().StringVar(&imageName, "image", imageName, "image name (default is executable name if != docker-runonce)")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
	rootCmd.Flags().BoolVar(&concurrentExecution, "concurrent", true, "allow concurrent execution")
}
