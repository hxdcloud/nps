package main

import (
	"context"
	"flag"
	"github.com/minio/minio-go/v7"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"hxdcloud/nps/lib/file"
	"hxdcloud/nps/lib/install"
	"hxdcloud/nps/lib/version"
	"hxdcloud/nps/server"
	"hxdcloud/nps/server/connection"
	"hxdcloud/nps/server/tool"
	"hxdcloud/nps/web/routers"

	"github.com/astaxie/beego"
	"github.com/astaxie/beego/logs"
	"hxdcloud/nps/lib/common"
	"hxdcloud/nps/lib/crypt"
	"hxdcloud/nps/lib/daemon"

	"github.com/kardianos/service"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/robfig/cron"
)

var (
	level string
	ver   = flag.Bool("version", false, "show current version")
)

func main() {
	flag.Parse()
	// init log
	if *ver {
		common.PrintVersion()
		return
	}
	if err := beego.LoadAppConfig("ini", filepath.Join(common.GetRunPath(), "conf", "nps.conf")); err != nil {
		log.Fatalln("load config file error", err.Error())
	}
	common.InitPProfFromFile()
	if level = beego.AppConfig.String("log_level"); level == "" {
		level = "7"
	}
	logs.Reset()
	logs.EnableFuncCallDepth(true)
	logs.SetLogFuncCallDepth(3)
	logPath := beego.AppConfig.String("log_path")
	if logPath == "" {
		logPath = common.GetLogPath()
	}
	if common.IsWindows() {
		logPath = strings.Replace(logPath, "\\", "\\\\", -1)
	}
	// init service
	options := make(service.KeyValue)
	svcConfig := &service.Config{
		Name:        "Nps",
		DisplayName: "nps内网穿透代理服务器",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      options,
	}
	svcConfig.Arguments = append(svcConfig.Arguments, "service")
	if len(os.Args) > 1 && os.Args[1] == "service" {
		_ = logs.SetLogger(logs.AdapterFile, `{"level":`+level+`,"filename":"`+logPath+`","daily":false,"maxlines":100000,"color":true}`)
	} else {
		_ = logs.SetLogger(logs.AdapterConsole, `{"level":`+level+`,"color":true}`)
	}
	if !common.IsWindows() {
		svcConfig.Dependencies = []string{
			"Requires=network.target",
			"After=network-online.target syslog.target"}
		svcConfig.Option["SystemdScript"] = install.SystemdScript
		svcConfig.Option["SysvScript"] = install.SysvScript
	}
	prg := &nps{}
	prg.exit = make(chan struct{})
	s, err := service.New(prg, svcConfig)
	if err != nil {
		logs.Error(err, "service function disabled")
		run()
		// run without service
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
		return
	}
	if len(os.Args) > 1 && os.Args[1] != "service" {
		switch os.Args[1] {
		case "reload":
			daemon.InitDaemon("nps", common.GetRunPath(), common.GetTmpPath())
			return
		case "install":
			// uninstall before
			_ = service.Control(s, "stop")
			_ = service.Control(s, "uninstall")

			binPath := install.InstallNps()
			svcConfig.Executable = binPath
			s, err := service.New(prg, svcConfig)
			if err != nil {
				logs.Error(err)
				return
			}
			err = service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				confPath := "/etc/init.d/" + svcConfig.Name
				os.Symlink(confPath, "/etc/rc.d/S90"+svcConfig.Name)
				os.Symlink(confPath, "/etc/rc.d/K02"+svcConfig.Name)
			}
			return
		case "start", "restart", "stop":
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				cmd := exec.Command("/etc/init.d/"+svcConfig.Name, os.Args[1])
				err := cmd.Run()
				if err != nil {
					logs.Error(err)
				}
				return
			}
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			return
		case "uninstall":
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				os.Remove("/etc/rc.d/S90" + svcConfig.Name)
				os.Remove("/etc/rc.d/K02" + svcConfig.Name)
			}
			return
		case "update":
			install.UpdateNps()
			return
		default:
			logs.Error("command is not support")
			return
		}
	}

	// 定时备份任务
	backUpConf()

	_ = s.Run()
}

type nps struct {
	exit chan struct{}
}

func (p *nps) Start(s service.Service) error {
	_, _ = s.Status()
	go p.run()
	return nil
}
func (p *nps) Stop(s service.Service) error {
	_, _ = s.Status()
	close(p.exit)
	if service.Interactive() {
		os.Exit(0)
	}
	return nil
}

func (p *nps) run() error {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logs.Warning("nps: panic serving %v: %v\n%s", err, string(buf))
		}
	}()
	run()
	select {
	case <-p.exit:
		logs.Warning("stop...")
	}
	return nil
}

func run() {
	routers.Init()
	task := &file.Tunnel{
		Mode: "webServer",
	}
	bridgePort, err := beego.AppConfig.Int("bridge_port")
	if err != nil {
		logs.Error("Getting bridge_port error", err)
		os.Exit(0)
	}
	logs.Info("the version of server is %s ,allow client core version to be %s", version.VERSION, version.GetVersion())
	connection.InitConnectionService()
	//crypt.InitTls(filepath.Join(common.GetRunPath(), "conf", "server.pem"), filepath.Join(common.GetRunPath(), "conf", "server.key"))
	crypt.InitTls()
	tool.InitAllowPort()
	tool.StartSystemInfo()
	timeout, err := beego.AppConfig.Int("disconnect_timeout")
	if err != nil {
		timeout = 60
	}
	go server.StartNewServer(bridgePort, task, beego.AppConfig.String("bridge_type"), timeout)
}

func backUpConf() {
	c := cron.New()
	c.AddFunc("0 * * * *", func() {
		log.Println("start back up conf...")
		backUpToMinio()
	})
	c.Start()
}

func backUpToMinio() {
	minioUrl := beego.AppConfig.String("minio_url")
	accessKeyID := beego.AppConfig.String("minio_access_key_id")
	secretAccessKey := beego.AppConfig.String("minio_secret_access_key")
	bucketName := beego.AppConfig.String("minio_bucket")
	minioDir := beego.AppConfig.String("minio_dir")
	conf := filepath.Join(common.GetRunPath(), "conf", "nps.conf")
	clients := filepath.Join(common.GetRunPath(), "conf", "clients.json")
	hosts := filepath.Join(common.GetRunPath(), "conf", "hosts.json")
	tasks := filepath.Join(common.GetRunPath(), "conf", "tasks.json")

	ctx := context.Background()
	endpoint := minioUrl
	useSSL := true

	// Initialize minio client object.
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatalln(err)
	}

	info1, err1 := minioClient.FPutObject(ctx, bucketName, minioDir+"/nps.conf", conf, minio.PutObjectOptions{ContentType: "application/conf"})
	if err1 != nil {
		log.Fatalln(err1)
	}
	log.Printf("Successfully uploaded %s of size %d\n", "nps.conf", info1.Size)

	info2, err2 := minioClient.FPutObject(ctx, bucketName, minioDir+"/clients.json", clients, minio.PutObjectOptions{ContentType: "application/json"})
	if err1 != nil {
		log.Fatalln(err2)
	}
	log.Printf("Successfully uploaded %s of size %d\n", "clients.conf", info2.Size)

	info3, err3 := minioClient.FPutObject(ctx, bucketName, minioDir+"/hosts.json", hosts, minio.PutObjectOptions{ContentType: "application/json"})
	if err1 != nil {
		log.Fatalln(err3)
	}
	log.Printf("Successfully uploaded %s of size %d\n", "hosts.conf", info3.Size)

	info4, err4 := minioClient.FPutObject(ctx, bucketName, minioDir+"/tasks.json", tasks, minio.PutObjectOptions{ContentType: "application/json"})
	if err1 != nil {
		log.Fatalln(err4)
	}
	log.Printf("Successfully uploaded %s of size %d\n", "tasks.conf", info4.Size)

}
