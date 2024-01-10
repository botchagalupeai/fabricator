package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
	"go.githedgehog.com/fabric/pkg/manager/config"
	"go.githedgehog.com/fabricator/pkg/fab"
	"go.githedgehog.com/fabricator/pkg/fab/cnc"
	"go.githedgehog.com/fabricator/pkg/fab/vlab"
	"go.githedgehog.com/fabricator/pkg/fab/wiring"
)

var version = "(devel)"

const (
	FLAG_CATEGORY_WIRING_GEN = "wiring generator options:"
)

func setupLogger(verbose, brief bool) error {
	if verbose && brief {
		return cli.Exit("verbose and brief are mutually exclusive", 1)
	}

	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	} else if brief {
		logLevel = slog.LevelWarn
	}

	logW := os.Stdout
	logger := slog.New(
		tint.NewHandler(logW, &tint.Options{
			Level:      logLevel,
			TimeFormat: time.TimeOnly,
			NoColor:    !isatty.IsTerminal(logW.Fd()),
		}),
	)
	slog.SetDefault(logger)

	slog.Debug("\n" + motd)
	slog.Debug("Version: " + version)

	return nil
}

//go:embed motd.txt
var motd string

func main() {
	var verbose, brief bool
	verboseFlag := &cli.BoolFlag{
		Name:        "verbose",
		Aliases:     []string{"v"},
		Usage:       "verbose output (includes debug)",
		Destination: &verbose,
	}
	briefFlag := &cli.BoolFlag{
		Name:        "brief",
		Aliases:     []string{"b"},
		Usage:       "brief output (only warn and error)",
		Destination: &brief,
	}

	var basedir, fromConfig, preset string
	var wiringPath cli.StringSlice
	basedirFlag := &cli.StringFlag{
		Name:        "basedir",
		Aliases:     []string{"d"},
		Usage:       "use workir `DIR`",
		Value:       ".hhfab",
		Destination: &basedir,
	}

	var presets []string
	for _, p := range fab.Presets {
		presets = append(presets, string(p))
	}

	var dryRun, hydrate, nopack bool

	var vm string
	vmFlag := &cli.StringFlag{
		Name:        "vm",
		Usage:       "use vm `VM-NAME`, use `control` for control vm",
		Destination: &vm,
	}

	var fabricMode string
	var wgChainControlLink bool
	var wgControlLinksCount, wgSpinesCount, wgFabricLinksCount, wgMCLAGLeafsCount, wgOrphanLeafsCount, wgMCLAGSessionLinks, wgMCLAGPeerLinks, wgVPCLoopbacks uint

	fabricModes := []string{}
	for _, m := range config.FabricModes {
		fabricModes = append(fabricModes, string(m))
	}

	wiringGenFlags := []cli.Flag{
		&cli.StringFlag{
			Name:        "fabric-mode",
			Aliases:     []string{"m"},
			Usage:       "fabric mode (one of: " + strings.Join(fabricModes, ", ") + ")",
			Destination: &fabricMode,
			Value:       string(config.FabricModeSpineLeaf),
		},
		&cli.BoolFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "chain-control-link",
			Usage:       "chain control links instead of all switches directly connected to control node if fabric mode is spine-leaf",
			Destination: &wgChainControlLink,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "control-links-count",
			Usage:       "number of control links if chain-control-link is enabled",
			Destination: &wgControlLinksCount,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "spines-count",
			Usage:       "number of spines if fabric mode is spine-leaf",
			Destination: &wgSpinesCount,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "fabric-links-count",
			Usage:       "number of fabric links if fabric mode is spine-leaf",
			Destination: &wgFabricLinksCount,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "mclag-leafs-count",
			Usage:       "number of mclag leafs (should be even)",
			Destination: &wgMCLAGLeafsCount,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "orphan-leafs-count",
			Usage:       "number of orphan leafs",
			Destination: &wgOrphanLeafsCount,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "mclag-session-links",
			Usage:       "number of mclag session links for each mclag leaf",
			Destination: &wgMCLAGSessionLinks,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "mclag-peer-links",
			Usage:       "number of mclag peer links for each mclag leaf",
			Destination: &wgMCLAGPeerLinks,
		},
		&cli.UintFlag{
			Category:    FLAG_CATEGORY_WIRING_GEN,
			Name:        "vpc-loopbacks",
			Usage:       "number of vpc loopbacks for each switch",
			Destination: &wgVPCLoopbacks,
		},
	}

	mngr := fab.NewCNCManager()

	extraInitFlags := append(wiringGenFlags, mngr.Flags()...)

	cli.VersionFlag.(*cli.BoolFlag).Aliases = []string{"V"}
	app := &cli.App{
		Name:                   "hhfab",
		Usage:                  "hedgehog fabricator - build, install and run hedgehog",
		Version:                version,
		Suggest:                true,
		UseShortOptionHandling: true,
		EnableBashCompletion:   true,
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "initialize fabricator with specified PRESET",
				Flags: append([]cli.Flag{
					basedirFlag,
					verboseFlag,
					briefFlag,
					&cli.StringFlag{
						Name:        "config",
						Aliases:     []string{"c"},
						Usage:       "start from existing config `FILE`",
						Destination: &fromConfig,
					},
					&cli.StringFlag{
						Name:        "preset",
						Aliases:     []string{"p"},
						Usage:       "use preset `PRESET` (one of: " + strings.Join(presets, ", ") + ")",
						Required:    true,
						Destination: &preset,
					},
					&cli.StringSliceFlag{
						Name:        "wiring",
						Aliases:     []string{"w"},
						Usage:       "use wiring diagram from `FILE` (or dir), use '-' to read from stdin, use multiple times to merge",
						Destination: &wiringPath,
					},
					&cli.BoolFlag{
						Name:        "hydrate",
						Usage:       "automatically hydrate wiring diagram if needed (if some IPs/ASN/etc missing)",
						Value:       true,
						Destination: &hydrate,
					},
				}, extraInitFlags...),
				Before: func(cCtx *cli.Context) error {
					return setupLogger(verbose, brief)
				},
				Action: func(cCtx *cli.Context) error {
					if fabricMode == "" {
						fabricMode = string(config.FabricModeSpineLeaf)
					}
					if !slices.Contains(fabricModes, fabricMode) {
						return errors.Errorf("invalid fabric mode %s (supported: %s)", fabricMode, strings.Join(fabricModes, ", "))
					}

					wiringGen := &wiring.Builder{
						FabricMode:        config.FabricMode(fabricMode),
						ChainControlLink:  wgChainControlLink,
						ControlLinksCount: uint8(wgControlLinksCount),
						SpinesCount:       uint8(wgSpinesCount),
						FabricLinksCount:  uint8(wgFabricLinksCount),
						MCLAGLeafsCount:   uint8(wgMCLAGLeafsCount),
						OrphanLeafsCount:  uint8(wgOrphanLeafsCount),
						MCLAGSessionLinks: uint8(wgMCLAGSessionLinks),
						MCLAGPeerLinks:    uint8(wgMCLAGPeerLinks),
						VPCLoopbacks:      uint8(wgVPCLoopbacks),
					}
					err := mngr.Init(basedir, fromConfig, cnc.Preset(preset), config.FabricMode(fabricMode), wiringPath.Value(), wiringGen, hydrate)
					if err != nil {
						return errors.Wrap(err, "error initializing")
					}

					return errors.Wrap(mngr.Save(), "error saving")
				},
			},
			{
				Name:  "build",
				Usage: "build bundles",
				Flags: []cli.Flag{
					basedirFlag,
					verboseFlag,
					briefFlag,
					&cli.BoolFlag{
						Name:        "nopack",
						Usage:       "do not pack bundles",
						Destination: &nopack,
					},
					// TODO support reset before build
					// &cli.BoolFlag{
					// 	Name:        "reset",
					// 	Usage:       "reset bundles in basedir before building",
					// 	Destination: &reset,
					// },
				},
				Before: func(ctx *cli.Context) error {
					return setupLogger(verbose, brief)
				},
				Action: func(cCtx *cli.Context) error {
					err := mngr.Load(basedir)
					if err != nil {
						return errors.Wrap(err, "error loading")
					}

					return errors.Wrap(mngr.Build(!nopack), "error building bundles")
				},
			},
			{
				Name:  "pack",
				Usage: "pack install bundles",
				Flags: []cli.Flag{
					basedirFlag,
					verboseFlag,
					briefFlag,
				},
				Before: func(ctx *cli.Context) error {
					return setupLogger(verbose, brief)
				},
				Action: func(cCtx *cli.Context) error {
					err := mngr.Load(basedir)
					if err != nil {
						return errors.Wrap(err, "error loading")
					}

					return errors.Wrap(mngr.Pack(), "error packing bundles")
				},
			},
			{
				Name:  "dump",
				Usage: "load fabricator and dump hydrated config",
				Flags: []cli.Flag{
					basedirFlag,
					verboseFlag,
					briefFlag,
				},
				Before: func(ctx *cli.Context) error {
					return setupLogger(verbose, brief)
				},
				Action: func(cCtx *cli.Context) error {
					err := mngr.Load(basedir)
					if err != nil {
						return errors.Wrap(err, "error loading")
					}

					return errors.Wrap(mngr.Dump(), "error dumping hydrated config")
				},
			},
			{
				Name:  "vlab",
				Usage: "fully virtual lab (VLAB) management",
				Flags: []cli.Flag{
					basedirFlag,
					verboseFlag,
					briefFlag,
				},
				Subcommands: []*cli.Command{
					{
						Name:  "up",
						Usage: "bring up vlab vms",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							&cli.BoolFlag{
								Name:        "dry-run",
								Usage:       "dry run, prepare vms but not actually run them",
								Destination: &dryRun,
							},
							&cli.BoolFlag{
								Name:  "kill-stale-vms",
								Usage: "kill stale vms before starting",
							},
							&cli.StringFlag{
								Name:  "vm-size",
								Usage: "run with one of the predefined sizes (one of: " + strings.Join(vlab.VM_SIZES, ", ") + ")",
							},
							&cli.BoolFlag{
								Name:  "install-complete",
								Usage: "run installer and complete vlab (for testing)",
							},
							&cli.StringFlag{
								Name:  "run-complete",
								Usage: "run installer, run provided script and than complete vlab (for testing)",
							},
							&cli.StringFlag{
								Name:  "ready-complete",
								Usage: "wait for switches ready, run provided script and than complete vlab (for testing)",
							},
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, cCtx.String("vm-size"))
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							killStaleVMs := cCtx.Bool("kill-stale-vms")

							return errors.Wrap(svc.StartServer(killStaleVMs,
								cCtx.Bool("install-complete"),
								cCtx.String("run-complete"),
								cCtx.String("ready-complete"),
							), "error starting vlab")
						},
					},
					{
						Name:  "ssh",
						Usage: "ssh to vm, args passed to ssh command, will use jump host if needed",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							vmFlag,
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.SSH(vm, cCtx.Args().Slice()), "error vm ssh")
						},
					},
					{
						Name:  "serial",
						Usage: "connect to vm serial console, no args for selector",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							vmFlag,
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.Serial(vm), "error vm serial")
						},
					},
					{
						Name:  "details",
						Usage: "list all vms with interactive detailed info",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							vmFlag,
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.List(), "error vm list")
						},
					},
					{
						Name:     "vfio-pci-bind",
						Category: "Hybrid (Baremetal switches + VMs)",
						Usage:    "bind all device used in vlab to vfio-pci driver for pci passthrough",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.VFIOPCIBindAll(), "error binding to vfio-pci")
						},
					},
					{
						Name:     "setup-vpcs",
						Category: "Testing",
						Usage:    "Setup VPCs and VPCAttachments for each server with valid connection and configure IP/VLAN on it",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							&cli.StringFlag{
								Name:  "type",
								Usage: "type of vpc setup, (one of: " + strings.Join(vlab.VPCSetupTypes, ", ") + ")",
								Value: vlab.VPCSetupTypeVPCPerServer,
							},
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.SetupVPCs(context.Background(), vlab.SetupVPCsConfig{
								Type: cCtx.String("type"),
							}), "error creating VPC per server")
						},
					},
					{
						Name:     "setup-peerings",
						Category: "Testing",
						Usage:    "Setup VPC and External Peerings, run with -h for more info",
						UsageText: strings.TrimSpace(strings.ReplaceAll(`
							Setup test scenario with VPC/External Peerings by specifying requests in the format described below.

							Example command:

							$ hhfab vlab setup-test-scenario 1+2 2+4:r=border 1~as5835 2~as5835:subnets=sub1,sub2:prefixes=0.0.0.0/0,22.22.22.0/24_le28

							Which will produce:
							1. VPC peering between vpc-1 and vpc-2
							2. Remote VPC peering between vpc-2 and vpc-4 on switch group named border
							3. External peering for vpc-1 with External as5835 with default vpc subnet and any routes from external permitted
							4. External peering for vpc-2 with External as5835 with subnets sub1 and sub2 exposed from vpc-2 and default route
							   from external permitted as well any route that belongs to 22.22.22.0/24 but <= /28

							VPC Peerings:

							1-2 -- VPC peering between vpc-1 and vpc-2
							1-2:r -- remote VPC peering between vpc-1 and vpc-2 on switch group if only one switch group is present
							1-2:r=border -- remote VPC peering between vpc-1 and vpc-2 on switch group named border
							1-2:remote=border -- same as above

							External Peerings:

							1~as5835 -- external peering for vpc-1 with External as5835
							1~ -- external peering for vpc-1 with external if only one external is present for ipv4 namespace of vpc-1, allowing
								default subnet and any route from external
							1~:subnets=default@prefixes=0.0.0.0/0 -- external peering for vpc-1 with auth external with default vpc subnet and
								default route from external permitted
							1~as5835:vpc_subnets=default,other:ext_prefixes=0.0.0.0/0_le32_ge32,22.22.22.0/24 -- same but with more details
						`, "							", "")),
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							&cli.BoolFlag{
								Name:  "dry-run",
								Usage: "print resulting setup, but don't apply",
							},
							&cli.BoolFlag{
								Name:  "cleanup",
								Usage: "cleanup all vpc/external peerings before setting up test scenario",
							},
							&cli.BoolFlag{
								Name:  "agent-check",
								Usage: "check if agent is running and legit on all servers",
								Value: true,
							},
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.SetupPeerings(context.Background(), vlab.SetupPeeringsConfig{
								AgentCheck: cCtx.Bool("agent-check"),
								DryRun:     cCtx.Bool("dry-run"),
								CleanupAll: cCtx.Bool("cleanup"),
								Requests:   cCtx.Args().Slice(),
							}), "error setting up test scenario")
						},
					},
					{
						Name:     "test-connectivity",
						Category: "Testing",
						Usage:    "Test connectivity for all present servers and externals based on peerings",
						Flags: []cli.Flag{
							basedirFlag,
							verboseFlag,
							briefFlag,
							&cli.BoolFlag{
								Name:  "invpc",
								Usage: "test connectivity beween servers in a single VPC",
								Value: true,
							},
							&cli.BoolFlag{
								Name:  "vpc",
								Usage: "test connectivity between servers in different VPCs",
								Value: true,
							},
							&cli.BoolFlag{
								Name:  "ext",
								Usage: "test external connectivity",
								Value: true,
							},
							&cli.UintFlag{
								Name:  "vpc-ping",
								Usage: "test VPC connectivity with ping, specify number of packets, 0 to disable",
								Value: 3,
							},
							&cli.UintFlag{
								Name:  "vpc-iperf",
								Usage: "test VPC connectivity with iperf, specify number of seconds, 0 to disable",
								Value: 10,
							},
							&cli.UintFlag{
								Name:  "vpc-iperf-speed",
								Usage: "iperf speed test threshold (Mbit/s), if speed is lower than this value, test will fail",
								Value: 8000,
							},
							&cli.BoolFlag{
								Name:  "ext-curl",
								Usage: "test external connectivity with curl (just 8.8.8.8)",
								Value: true,
							},
							&cli.BoolFlag{
								Name:  "agent-check",
								Usage: "check if agent is running and legit on all servers",
								Value: true,
							},
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := mngr.Load(basedir)
							if err != nil {
								return errors.Wrap(err, "error loading")
							}

							svc, err := fab.LoadVLAB(basedir, mngr, dryRun, "")
							if err != nil {
								return errors.Wrap(err, "error loading vlab")
							}

							return errors.Wrap(svc.TestConnectivity(context.Background(), vlab.ServerConnectivityTestConfig{
								AgentCheck:    cCtx.Bool("agent-check"),
								InVPC:         cCtx.Bool("invpc"),
								VPC:           cCtx.Bool("vpc"),
								Ext:           cCtx.Bool("ext"),
								VPCPing:       cCtx.Uint("vpc-ping"),
								VPCIperf:      cCtx.Uint("vpc-iperf"),
								VPCIperfSpeed: cCtx.Uint("vpc-iperf-speed"),
								ExtCurl:       cCtx.Bool("ext-curl"),
							}), "error testing server connectivity")
						},
					},
				},
			},
			{
				Name:  "wiring",
				Usage: "tools for working with wiring diagram",
				Flags: []cli.Flag{
					basedirFlag,
					verboseFlag,
					briefFlag,
				},
				Subcommands: []*cli.Command{
					{
						Name:  "sample",
						Usage: "sample wiring diagram (would work for vlab)",
						Flags: append([]cli.Flag{
							verboseFlag,
							briefFlag,
						}, wiringGenFlags...),
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							if fabricMode == "" {
								fabricMode = string(config.FabricModeSpineLeaf)
							}
							if !slices.Contains(fabricModes, fabricMode) {
								return errors.Errorf("invalid fabric mode %s (supported: %s)", fabricMode, strings.Join(fabricModes, ", "))
							}

							data, err := (&wiring.Builder{
								FabricMode:        config.FabricMode(fabricMode),
								ChainControlLink:  wgChainControlLink,
								ControlLinksCount: uint8(wgControlLinksCount),
								SpinesCount:       uint8(wgSpinesCount),
								FabricLinksCount:  uint8(wgFabricLinksCount),
								MCLAGLeafsCount:   uint8(wgMCLAGLeafsCount),
								OrphanLeafsCount:  uint8(wgOrphanLeafsCount),
								MCLAGSessionLinks: uint8(wgMCLAGSessionLinks),
								MCLAGPeerLinks:    uint8(wgMCLAGPeerLinks),
								VPCLoopbacks:      uint8(wgVPCLoopbacks),
							}).Build()
							if err != nil {
								return errors.Wrap(err, "error building sample")
							}

							return errors.Wrapf(data.Write(os.Stdout), "error writing sample")
						},
					},
					{
						Name:  "hydrate",
						Usage: "hydrate wiring diagram",
						Flags: []cli.Flag{
							verboseFlag,
							briefFlag,
							&cli.StringFlag{
								Name:    "wiring",
								Aliases: []string{"w"},
								Usage:   "use wiring `FILE`",
							},
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							err := wiring.HydratePath(cCtx.String("wiring"))
							if err != nil {
								return errors.Wrap(err, "error hydrating")
							}

							return nil
						},
					},
					{
						Name:  "graph",
						Usage: "generate dot graph from wiring diagram (experimental)",
						Flags: []cli.Flag{
							verboseFlag,
							briefFlag,
							&cli.StringFlag{
								Name:    "wiring",
								Aliases: []string{"w"},
								Usage:   "use wiring `FILE`",
							},
						},
						Before: func(ctx *cli.Context) error {
							return setupLogger(verbose, brief)
						},
						Action: func(cCtx *cli.Context) error {
							data, err := wiring.Visualize(cCtx.String("wiring"))
							if err != nil {
								return errors.Wrap(err, "error visualizing")
							}

							fmt.Println(data)

							return nil
						},
					},
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		slog.Error("Failed", "err", err)
		os.Exit(1)
	}
}
