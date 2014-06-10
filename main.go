package main

import (
        "fmt"
        "github.com/Sirupsen/logrus"
        "github.com/buger/goterm"
        "github.com/docopt/docopt.go"
        "html/template"
        "net/http"
        "net/http/httputil"
        "net/url"
        "os"
        "strconv"
)

var log = logrus.New()
var authManager = AuthorisationManager{}
var sessionLimiter = SessionLimiter{}
var config = Config{}
var templates = &template.Template{}
var systemError string = "{\"status\": \"system error, please contact administrator\"}"
var analytics = RedisAnalyticsHandler{}
var prof_file = &os.File{}
var doMemoryProfile bool

func displayConfig() {
        config_table := goterm.NewTable(0, 10, 5, ' ', 0)
        fmt.Fprintf(config_table, "Listening on port:\t%d\n", config.ListenPort)
        fmt.Fprintf(config_table, "Source path:\t%s\n", config.ListenPath)
        fmt.Fprintf(config_table, "Gateway target:\t%s\n", config.TargetUrl)

        fmt.Println(config_table)
        fmt.Println("")
}

func setupGlobals() {
        if config.Storage.Type == "memory" {
                log.Warning("Using in-memory storage. Warning: this is not scalable.")
                authManager = AuthorisationManager{
                        &InMemoryStorageManager{
                                map[string]string{}}}
        } else if config.Storage.Type == "redis" {
                log.Info("Using Redis storage manager.")
                authManager = AuthorisationManager{
                        &RedisStorageManager{KeyPrefix: "apikey-"}}

                authManager.Store.Connect()
        }

        if (config.EnableAnalytics == true) && (config.Storage.Type != "redis") {
                log.Panic("Analytics requires Redis Storage backend, please enable Redis in the tyk.conf file.")
        }

        if config.EnableAnalytics {
                AnalyticsStore := RedisStorageManager{KeyPrefix: "analytics-"}
                log.Info("Setting up analytics DB connection")

                if config.AnalyticsConfig.Type == "csv" {
                        log.Info("Using CSV cache purge")
                        analytics = RedisAnalyticsHandler{
                                Store:  &AnalyticsStore,
                                Clean:  &CSVPurger{&AnalyticsStore}}

                } else if config.AnalyticsConfig.Type == "mongo" {
                        log.Info("Using MongoDB cache purge")
                        analytics = RedisAnalyticsHandler{
                                Store:  &AnalyticsStore,
                                Clean:  &MongoPurger{&AnalyticsStore, nil}}
                }

                analytics.Store.Connect()
                go analytics.Clean.StartPurgeLoop(config.AnalyticsConfig.PurgeDelay)
        }

        template_file := fmt.Sprintf("%s/error.json", config.TemplatePath)
        templates = template.Must(template.ParseFiles(template_file))
}

func init() {
        usage := `Tyk API Gateway.

	Usage:
		tyk [options]

	Options:
		-h --help      Show this screen
		--conf=FILE    Load a named configuration file
		--port=PORT    Listen on PORT (overrides confg file)
		--memprofile   Generate a memory profile

	`

        arguments, err := docopt.Parse(usage, nil, true, "Tyk v1.0", false)
        if err != nil {
                log.Println("Error while parsing arguments.")
                log.Fatal(err)
        }

        filename := "tyk.conf"
        value, _ := arguments["--conf"]
        if value != nil {
                log.Info(fmt.Sprintf("Using %s for configuration", value.(string)))
                filename = arguments["--conf"].(string)
        } else {
                log.Info("No configuration file defined, will try to use default (./tyk.conf)")
        }

        loadConfig(filename, &config)

        setupGlobals()
        port, _ := arguments["--port"]
        if port != nil {
                portNum, err := strconv.Atoi(port.(string))
                if err != nil {
                        log.Error("Port specified in flags must be a number!")
                        log.Error(err)
                } else {
                        config.ListenPort = portNum
                }
        }

        doMemoryProfile, _ = arguments["--memprofile"].(bool)

}

func intro() {
        fmt.Print("\n\n")
        fmt.Println(goterm.Bold(goterm.Color("Tyk.io Gateway API v0.1", goterm.GREEN)))
        fmt.Println(goterm.Bold(goterm.Color("=======================", goterm.GREEN)))
        fmt.Print("Copyright Jively Ltd. 2014")
        fmt.Print("\nhttp://www.tyk.io\n\n")
}

func main() {
        intro()
        displayConfig()

        if doMemoryProfile {
                log.Info("Memory profiling active")
                prof_file, _ = os.Create("tyk.mprof")
                defer prof_file.Close()
        }

        remote, err := url.Parse(config.TargetUrl)
        if err != nil {
                log.Error("Culdn't parse target URL")
                log.Error(err)
        }

        proxy := httputil.NewSingleHostReverseProxy(remote)
        http.HandleFunc("/tyk/keys/create", securityHandler(createKeyHandler))
        http.HandleFunc("/tyk/keys/", securityHandler(keyHandler))
        http.HandleFunc(config.ListenPath, handler(proxy))
        targetPort := fmt.Sprintf(":%d", config.ListenPort)
        err = http.ListenAndServe(targetPort, nil)
        if err != nil {
                log.Error(err)
        }

}
