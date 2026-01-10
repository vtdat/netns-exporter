package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netns"
)

const (
	NetnsPath          = "/run/netns/"
	InterfaceStatPath  = "/sys/class/net/" // standard symlink path
	ProcThreadStatPath = "/proc/thread-self/"
	ProcStatPath       = "/proc/"

	collectorNamespace = "netns"
	collectorSubsystem = "network"

	// Label names
	netnsLabel  = "netns"
	deviceLabel = "device"
	hostLabel   = "host"
	ipLabel     = "deviceIP"
	typeLabel   = "type"

	// Conntrack Metrics
	metricConntrackTotal = "conntrack_total"
	metricConntrackMax   = "conntrack_max"
	conntrackTotalPath   = "sys/net/netfilter/nf_conntrack_count"
	conntrackMaxPath     = "sys/net/netfilter/nf_conntrack_max"

	// SNMP Metrics
	metricTcpRetransSegs = "tcp_retrans_segs_total"
	metricTcpInErrs      = "tcp_in_errs_total"
	metricUdpInErrors    = "udp_in_errors_total"
	metricUdpNoPorts     = "udp_no_ports_total"

	// Sockstat Metrics
	metricSocketsUsed = "sockets_used"
	metricTcpInUse    = "tcp_sockets_inuse"
	metricTcpTimeWait = "tcp_sockets_tw"
	metricTcpMem      = "tcp_sockets_mem"
	metricUdpInUse    = "udp_sockets_inuse"

	// Ping Metrics
	metricPingSuccessRate    = "ping_success_rate"
	metricPingAverageLatency = "ping_average_latency_ms"

	// ARP Metrics
	metricArpEntries = "arp_entries"
)

type Collector struct {
	logger          logrus.FieldLogger
	config          *NetnsExporterConfig
	nsMetrics       *prometheus.Desc
	intfMetrics     map[string]*prometheus.Desc
	ctMetrics       map[string]*prometheus.Desc
	snmpMetrics     map[string]*prometheus.Desc
	sockstatMetrics map[string]*prometheus.Desc
	pingMetrics     map[string]*prometheus.Desc
	arpMetric       *prometheus.Desc
	hostname        string
	cache           *MetricCache
}

func NewCollector(config *NetnsExporterConfig, logger *logrus.Logger, cache *MetricCache) *Collector {
	// Pre-compute hostname once to save syscalls
	hostname, _ := os.Hostname()

	nsMetrics := prometheus.NewDesc(
		prometheus.BuildFQName(collectorNamespace, collectorSubsystem, "namespaces_total"),
		"Total number of network namespaces found",
		[]string{hostLabel}, nil,
	)
	// Add descriptions for interface metrics
	intfMetrics := make(map[string]*prometheus.Desc, len(config.InterfaceMetrics))
	for _, metric := range config.InterfaceMetrics {
		intfMetrics[metric] = prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metric+"_total"),
			"Interface statistics in the network namespace",
			[]string{netnsLabel, deviceLabel, typeLabel, hostLabel, ipLabel},
			nil,
		)
	}

	// Add descriptions for ct metrics
	ctMetrics := map[string]*prometheus.Desc{
		"Conntrack_Total": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricConntrackTotal),
			"Number of NAT connection tracking entries in the network namespace",
			[]string{netnsLabel, typeLabel, hostLabel},
			nil,
		),
		"Conntrack_Max": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricConntrackMax),
			"Maximum number of NAT connection tracking entries in the network namespace",
			[]string{netnsLabel, typeLabel, hostLabel},
			nil,
		),
	}

	// Initialize SNMP Descriptors
	snmpMetrics := map[string]*prometheus.Desc{
		"Tcp_RetransSegs": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricTcpRetransSegs),
			"Total TCP segments retransmitted",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"Tcp_InErrs": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricTcpInErrs),
			"Total TCP segments received in error",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"Udp_InErrors": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricUdpInErrors),
			"Total UDP packets received with errors",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"Udp_NoPorts": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricUdpNoPorts),
			"Total UDP packets received on closed ports",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
	}

	// Initialize Sockstat Descriptors
	sockstatMetrics := map[string]*prometheus.Desc{
		"sockets_used": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricSocketsUsed),
			"Total used sockets",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"TCP_inuse": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricTcpInUse),
			"TCP sockets currently in use",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"TCP_tw": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricTcpTimeWait),
			"TCP sockets in TimeWait state",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"TCP_mem": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricTcpMem),
			"Kernel memory used by TCP buffers (in pages)",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
		"UDP_inuse": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricUdpInUse),
			"UDP sockets currently in use",
			[]string{netnsLabel, typeLabel, hostLabel}, nil,
		),
	}

	// Initialize Ping Descriptors
	pingMetrics := map[string]*prometheus.Desc{
		"Ping_SuccessRate": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricPingSuccessRate),
			"Percentage of successful ping attempts to destination host",
			[]string{netnsLabel, deviceLabel, typeLabel, hostLabel, "destination"}, nil,
		),
		"Ping_AverageLatency": prometheus.NewDesc(
			prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricPingAverageLatency),
			"Average latency in milliseconds for successful pings to destination host",
			[]string{netnsLabel, deviceLabel, typeLabel, hostLabel, "destination"}, nil,
		),
	}

	// Initialize ARP Descriptor
	arpMetric := prometheus.NewDesc(
		prometheus.BuildFQName(collectorNamespace, collectorSubsystem, metricArpEntries),
		"ARP table entries in qrouter namespace",
		[]string{netnsLabel, typeLabel, hostLabel, "ip_address", "hw_address", "device", "state"}, nil,
	)

	return &Collector{
		logger:          logger.WithField("component", "collector"),
		config:          config,
		nsMetrics:       nsMetrics,
		intfMetrics:     intfMetrics,
		ctMetrics:       ctMetrics,
		snmpMetrics:     snmpMetrics,
		sockstatMetrics: sockstatMetrics,
		pingMetrics:     pingMetrics,
		arpMetric:       arpMetric,
		hostname:        hostname,
		cache:           cache,
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.nsMetrics
	for _, desc := range c.intfMetrics {
		ch <- desc
	}
	for _, desc := range c.ctMetrics {
		ch <- desc
	}
	for _, desc := range c.snmpMetrics {
		ch <- desc
	}
	for _, desc := range c.sockstatMetrics {
		ch <- desc
	}
	for _, desc := range c.pingMetrics {
		ch <- desc
	}
	ch <- c.arpMetric
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	// Get cached metric data
	cachedData, cacheTime := c.cache.GetMetricData()

	c.logger.Debugf("Serving %d cached metrics from %s (age: %s)",
		len(cachedData),
		cacheTime.Format(time.RFC3339),
		c.cache.GetCacheAge())

	// Recreate metrics from cached data and send to Prometheus
	for _, data := range cachedData {
		// Find the appropriate descriptor and value type
		desc, valueType := c.getDescriptorForMetric(data.Desc)

		if desc != nil {
			ch <- prometheus.MustNewConstMetric(desc, valueType, data.Value, data.LabelValues...)
		}
	}
}

// getDescriptorForMetric returns the descriptor and value type for a given metric name
func (c *Collector) getDescriptorForMetric(metricName string) (*prometheus.Desc, prometheus.ValueType) {
	// Check namespace metric
	if metricName == "namespaces_total" {
		return c.nsMetrics, prometheus.GaugeValue
	}

	// Check ARP metric
	if metricName == "arp_entries" {
		return c.arpMetric, prometheus.GaugeValue
	}

	// Check interface metrics
	for name, desc := range c.intfMetrics {
		if metricName == name {
			return desc, prometheus.CounterValue
		}
	}

	// Check conntrack metrics
	for name, desc := range c.ctMetrics {
		if metricName == name {
			return desc, prometheus.GaugeValue
		}
	}

	// Check SNMP metrics
	for name, desc := range c.snmpMetrics {
		if metricName == name {
			return desc, prometheus.CounterValue
		}
	}

	// Check sockstat metrics
	for name, desc := range c.sockstatMetrics {
		if metricName == name {
			return desc, prometheus.GaugeValue
		}
	}

	// Check ping metrics
	for name, desc := range c.pingMetrics {
		if metricName == name {
			return desc, prometheus.GaugeValue
		}
	}

	return nil, prometheus.UntypedValue
}

// collectMetrics performs the actual metric collection from all namespaces
// This is called periodically by the cache's background goroutine
// Returns collected metric data
func (c *Collector) collectMetrics() []CachedMetricData {
	startTime := time.Now()
	metricData := make([]CachedMetricData, 0)

	// 1. Get Namespace Files
	nsFiles, err := os.ReadDir(NetnsPath)
	if err != nil {
		c.logger.Errorf("Failed to read namespace dir %s: %v", NetnsPath, err)
		return metricData
	}

	// Add namespace count metric
	metricData = append(metricData, CachedMetricData{
		Desc:        "namespaces_total",
		Value:       float64(len(nsFiles)),
		LabelValues: []string{c.hostname},
	})

	// 2. Setup Concurrency Control
	wg := NewLimitedWaitGroup(c.config.Threads)

	// Use a mutex to protect metricData slice
	var mu sync.Mutex

	c.logger.Debugf("Found %d namespaces. Using %d threads.", len(nsFiles), c.config.Threads)

	for _, nsFile := range nsFiles {
		name := nsFile.Name()

		// 3. Filter Namespace
		if !c.config.NamespacesFilter.IsAllowed(name) {
			c.logger.Debugf("Skipping namespace %s (filtered)", name)
			continue
		}

		// 4. Start Worker
		wg.Add(1)
		go func(nsName string) {
			defer wg.Done()
			nsMetrics := c.collectNamespace(nsName)

			// Append to shared slice with mutex protection
			mu.Lock()
			metricData = append(metricData, nsMetrics...)
			mu.Unlock()
		}(name)
	}

	wg.Wait()
	c.logger.Debugf("Collection cycle took %s", time.Since(startTime))

	return metricData
}

// collectNamespace runs in a separate goroutine.
// It locks the OS thread, switches namespace, collects metrics, and cleans up.
// Returns collected metric data for this namespace
func (c *Collector) collectNamespace(namespace string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	// 1. Lock OS Thread (CRITICAL)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 2. Save Host Namespace (to restore later)
	originalNS, err := netns.Get()
	if err != nil {
		c.logger.Errorf("Failed to get current namespace: %v", err)
		return metricData
	}
	defer originalNS.Close()

	// Safety: Always restore host namespace before unlocking the thread
	defer func() {
		_ = netns.Set(originalNS)
	}()

	// 3. Open Target Namespace
	targetNS, err := netns.GetFromName(namespace)
	if err != nil {
		c.logger.Warnf("Failed to get namespace handle for %s: %v", namespace, err)
		return metricData
	}
	defer targetNS.Close()

	// 4. Switch to Target Namespace
	if err := netns.Set(targetNS); err != nil {
		c.logger.Errorf("Failed to switch to namespace %s: %v", namespace, err)
		return metricData
	}

	// 5. Create Private Mount Namespace (Sandbox)
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		c.logger.Errorf("Unshare failed for %s: %v", namespace, err)
		return metricData
	}

	// Prevent mount propagation back to the host
	if err := syscall.Mount("", "/", "none", syscall.MS_SLAVE|syscall.MS_REC, ""); err != nil {
		c.logger.Errorf("Failed to remount root as slave in %s: %v", namespace, err)
		return metricData
	}

	// 6. Mount /sys
	_ = syscall.Unmount("/sys", syscall.MNT_DETACH)

	if err := syscall.Mount("sysfs", "/sys", "sysfs", syscall.MS_RDONLY, ""); err != nil {
		c.logger.Warnf("Failed to mount sysfs for %s: %v", namespace, err)
		return metricData
	}
	defer syscall.Unmount("/sys", syscall.MNT_DETACH)

	// 7. Collect Metrics
	if c.config.EnabledMetrics.Interface {
		metricData = append(metricData, c.collectInterfaces(namespace)...)
	}

	if c.config.EnabledMetrics.Conntrack {
		metricData = append(metricData, c.collectCtMetrics(namespace)...)
	}

	if c.config.EnabledMetrics.SNMP {
		metricData = append(metricData, c.collectSnmpMetrics(namespace)...)
	}

	if c.config.EnabledMetrics.Sockstat {
		metricData = append(metricData, c.collectSockstatMetrics(namespace)...)
	}

	if c.config.EnabledMetrics.ARP && getType(namespace) == "qrouter" {
		metricData = append(metricData, c.collectArpMetrics(namespace)...)
	}

	return metricData
}

func (c *Collector) collectInterfaces(namespace string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	ifFiles, err := os.ReadDir(InterfaceStatPath)
	if err != nil {
		c.logger.Errorf("Failed to read %s in namespace %s: %v", InterfaceStatPath, namespace, err)
		return metricData
	}

	for _, ifFile := range ifFiles {
		devName := ifFile.Name()

		// Skip loopback and filtered devices
		if devName == "lo" || !c.config.DeviceFilter.IsAllowed(devName) {
			continue
		}

		// Get IP
		deviceIP, err := c.getIPv4Address(devName)
		if err != nil {
			c.logger.Debugf("Could not get IP for %s in %s: %v", devName, namespace, err)
		}

		if deviceIP == "" {
			continue
		}

		// Check if IP is external and collect ping metrics
		if c.config.EnabledMetrics.Ping && deviceIP != "" && !c.isIPInInternalCIDRs(deviceIP) && getType(namespace) == "qrouter" {
			metricData = append(metricData, c.collectPingMetrics(namespace, deviceIP)...)
		}

		// Read metrics
		for metricName := range c.intfMetrics {
			val := c.readFloatFromFile(InterfaceStatPath + devName + "/statistics/" + metricName)
			if val == -1 {
				continue
			}

			metricData = append(metricData, CachedMetricData{
				Desc:        metricName,
				Value:       val,
				LabelValues: []string{namespace, devName, getType(namespace), c.hostname, deviceIP},
			})
		}
	}

	return metricData
}

// collectPingMetrics handles ping monitoring for external IP addresses
func (c *Collector) collectPingMetrics(namespace string, deviceIP string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	// Ensure log directory exists
	if err := c.ensurePingLogDirectory(namespace); err != nil {
		c.logger.Warnf("Failed to create log directory for namespace %s: %v", namespace, err)
		return metricData
	}

	logPath := c.getPingLogPath(namespace)

	// Check if log file exists
	_, err := os.Stat(logPath)
	if os.IsNotExist(err) {
		// File doesn't exist; spawn ping process
		c.logger.Debugf("Creating ping log for namespace %s to destination %s", namespace, c.config.DestinationHost)
		c.spawnPingProcess(namespace, c.config.DestinationHost)
		return metricData
	}

	// Parse existing log results
	successRate, avgLatency, err := c.parsePingLogResults(logPath)
	if err != nil {
		c.logger.Debugf("Failed to parse ping log for namespace %s: %v", namespace, err)
		return metricData
	}

	// Add success rate metric
	metricData = append(metricData, CachedMetricData{
		Desc:        "Ping_SuccessRate",
		Value:       successRate,
		LabelValues: []string{namespace, deviceIP, getType(namespace), c.hostname, c.config.DestinationHost},
	})

	// Add average latency metric
	metricData = append(metricData, CachedMetricData{
		Desc:        "Ping_AverageLatency",
		Value:       avgLatency,
		LabelValues: []string{namespace, deviceIP, getType(namespace), c.hostname, c.config.DestinationHost},
	})

	// Spawn new ping process asynchronously
	c.spawnPingProcess(namespace, c.config.DestinationHost)

	return metricData
}

// collectCtMetrics collects conntrack metrics
func (c *Collector) collectCtMetrics(namespace string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	metricPaths := map[string]string{
		"Conntrack_Total": conntrackTotalPath,
		"Conntrack_Max":   conntrackMaxPath,
	}

	for metricName, metricPath := range metricPaths {
		val := c.readFloatFromFile(ProcStatPath + metricPath)
		if val == -1 {
			continue
		}

		metricData = append(metricData, CachedMetricData{
			Desc:        metricName,
			Value:       val,
			LabelValues: []string{namespace, getType(namespace), c.hostname},
		})
	}

	return metricData
}

// readFloatFromFile reads a single float from a file. Returns -1 on error.
func (c *Collector) readFloatFromFile(path string) float64 {
	data, err := os.ReadFile(path)
	if err != nil {
		c.logger.Debugf("Failed to read file %s: %v", path, err)
		return -1
	}

	val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		c.logger.Warnf("Failed to parse float from %s: %v", path, err)
		return -1
	}
	return val
}

// collectSnmpMetrics collects SNMP-related metrics from /proc/net/snmp
func (c *Collector) collectSnmpMetrics(namespace string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	snmpFile := ProcThreadStatPath + "net/snmp"
	data, err := os.ReadFile(snmpFile)
	if err != nil {
		c.logger.Errorf("Failed to read SNMP file %s in namespace %s: %v", snmpFile, namespace, err)
		return metricData
	}

	lines := strings.Split(string(data), "\n")
	snmpData := make(map[string]map[string]float64)

	// Parse SNMP file into a map
	for i := 0; i < len(lines)-1; i += 2 {
		header := strings.Fields(lines[i])
		values := strings.Fields(lines[i+1])
		if len(header) != len(values) {
			continue
		}
		proto := strings.TrimSuffix(header[0], ":")
		snmpData[proto] = make(map[string]float64)
		for j := 1; j < len(header); j++ {
			val, err := strconv.ParseFloat(values[j], 64)
			if err != nil {
				continue
			}
			snmpData[proto][header[j]] = val
		}
	}

	// Map metrics to descriptors
	metricMap := map[string]struct {
		proto string
		field string
	}{
		"Tcp_RetransSegs": {"Tcp", "RetransSegs"},
		"Tcp_InErrs":      {"Tcp", "InErrs"},
		"Udp_InErrors":    {"Udp", "InErrors"},
		"Udp_NoPorts":     {"Udp", "NoPorts"},
	}

	for metricName, info := range metricMap {
		if val, ok := snmpData[info.proto][info.field]; ok {
			metricData = append(metricData, CachedMetricData{
				Desc:        metricName,
				Value:       val,
				LabelValues: []string{namespace, getType(namespace), c.hostname},
			})
		}
	}

	return metricData
}

// collectSockstatMetrics collects socket statistics from /proc/net/sockstat
func (c *Collector) collectSockstatMetrics(namespace string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	sockstatFile := ProcThreadStatPath + "net/sockstat"
	data, err := os.ReadFile(sockstatFile)
	if err != nil {
		c.logger.Errorf("Failed to read sockstat file %s in namespace %s: %v", sockstatFile, namespace, err)
		return metricData
	}

	lines := strings.Split(string(data), "\n")
	sockstatData := make(map[string]map[string]float64)

	// Parse sockstat file into a map
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		proto := strings.TrimSuffix(fields[0], ":")
		sockstatData[proto] = make(map[string]float64)
		for i := 1; i < len(fields)-1; i += 2 {
			val, err := strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				continue
			}
			sockstatData[proto][fields[i]] = val
		}
	}

	// Map metrics to descriptors
	metricMap := map[string]struct {
		proto string
		field string
	}{
		"sockets_used": {"sockets", "used"},
		"TCP_inuse":    {"TCP", "inuse"},
		"TCP_tw":       {"TCP", "tw"},
		"TCP_mem":      {"TCP", "mem"},
		"UDP_inuse":    {"UDP", "inuse"},
	}

	for metricName, info := range metricMap {
		if val, ok := sockstatData[info.proto][info.field]; ok {
			metricData = append(metricData, CachedMetricData{
				Desc:        metricName,
				Value:       val,
				LabelValues: []string{namespace, getType(namespace), c.hostname},
			})
		}
	}

	return metricData
}

// collectArpMetrics collects ARP table entries from /proc/net/arp for qrouter namespaces
func (c *Collector) collectArpMetrics(namespace string) []CachedMetricData {
	metricData := make([]CachedMetricData, 0)

	arpFile := ProcThreadStatPath + "net/arp"
	data, err := os.ReadFile(arpFile)
	if err != nil {
		c.logger.Debugf("Failed to read ARP file %s in namespace %s: %v", arpFile, namespace, err)
		return metricData
	}

	lines := strings.Split(string(data), "\n")
	// Skip header line (first line)
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		ipAddress := fields[0]
		hwAddress := fields[3]
		device := fields[5]

		// Parse flags to determine state
		flags := fields[2]
		state := "incomplete"
		switch flags {
		case "0x2":
			state = "reachable"
		case "0x6":
			state = "stale"
		case "0x0":
			state = "incomplete"
		}

		metricData = append(metricData, CachedMetricData{
			Desc:        "arp_entries",
			Value:       1,
			LabelValues: []string{namespace, getType(namespace), c.hostname, ipAddress, hwAddress, device, state},
		})
	}

	return metricData
}

// getIPv4Address retrieves the first IPv4 address of the interface.
func (c *Collector) getIPv4Address(device string) (string, error) {
	iface, err := net.InterfaceByName(device)
	if err != nil {
		return "", err
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}

	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}

		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String(), nil
		}
	}

	return "", fmt.Errorf("no ipv4 address found")
}

// getType returns the type label based on namespace name
func getType(namespace string) string {
	if strings.HasPrefix(namespace, "qrouter-") {
		return "qrouter"
	} else if strings.HasPrefix(namespace, "qdhcp-") {
		return "qdhcp"
	}
	return "other"
}

// isIPInInternalCIDRs checks if an IP address is within any of the configured internal CIDR ranges
func (c *Collector) isIPInInternalCIDRs(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, cidr := range c.config.InternalCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			c.logger.Debugf("Invalid CIDR in config: %s: %v", cidr, err)
			continue
		}
		if ipNet.Contains(parsedIP) {
			return true
		}
	}
	return false
}

// ensurePingLogDirectory creates the log directory if it doesn't exist
func (c *Collector) ensurePingLogDirectory(namespace string) error {
	logDir := c.config.LogDirectory + "/" + namespace
	return os.MkdirAll(logDir, 0755)
}

// getPingLogPath returns the full path to the ping log file for a namespace
func (c *Collector) getPingLogPath(namespace string) string {
	return c.config.LogDirectory + "/" + namespace + "/ping_log"
}

// appendPingResult appends a ping result to the log file with timestamp
func (c *Collector) appendPingResult(logPath string, result string) error {
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open ping log file: %w", err)
	}
	defer file.Close()

	timestamp := time.Now().Format(time.RFC3339)
	_, err = file.WriteString(timestamp + " " + result + "\n")
	return err
}

// parsePingLogResults reads the recent ping results from log file and calculates success rate and latency
func (c *Collector) parsePingLogResults(logPath string) (float64, float64, error) {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read ping log: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, 0, fmt.Errorf("no ping results in log file")
	}

	// Get the most recent lines (up to scrape_interval count)
	startIdx := 0
	if len(lines) > c.config.ScrapeInterval {
		startIdx = len(lines) - c.config.ScrapeInterval
	}
	recentLines := lines[startIdx:]

	successCount := 0
	totalLatency := float64(0)
	validResultCount := 0

	for _, line := range recentLines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		status := parts[1]
		latencyStr := parts[2]

		validResultCount++

		if status == "success" {
			successCount++
			latency, err := strconv.ParseFloat(latencyStr, 64)
			if err != nil {
				continue
			}
			totalLatency += latency
		}
	}

	if validResultCount == 0 {
		return 0, 0, fmt.Errorf("no valid ping results in log")
	}

	successRate := (float64(successCount) / float64(validResultCount)) * 100
	averageLatency := float64(0)
	if successCount > 0 {
		averageLatency = totalLatency / float64(successCount)
	}

	return successRate, averageLatency, nil
}

// spawnPingProcess spawns a background ping process for external IPs
func (c *Collector) spawnPingProcess(namespace string, destination string) {
	go func() {
		logPath := c.getPingLogPath(namespace)

		pingCount := strconv.Itoa(c.config.ScrapeInterval)
		cmd := exec.Command("ip", "netns", "exec", namespace, "ping", "-c", pingCount, "-i", "1", "-W", "2", destination)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			c.logger.Debugf("Failed to create stdout pipe for namespace %s: %v", namespace, err)
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			c.logger.Debugf("Failed to create stderr pipe for namespace %s: %v", namespace, err)
			return
		}

		if err := cmd.Start(); err != nil {
			c.logger.Debugf("Failed to start ping process for namespace %s: %v", namespace, err)
			return
		}

		expectedPings := c.config.ScrapeInterval
		receivedPings := 0

		scanner := bufio.NewScanner(stdout)
		go func() {
			for scanner.Scan() {
				line := scanner.Text()
				if strings.Contains(line, "time=") {
					receivedPings++
					latency := extractLatencyFromPingOutput(line)
					if appendErr := c.appendPingResult(logPath, fmt.Sprintf("success %.2f", latency)); appendErr != nil {
						c.logger.Debugf("Failed to append ping result for namespace %s: %v", namespace, appendErr)
					}
				} else if strings.Contains(line, "Destination Host Unreachable") {
					receivedPings++
					if appendErr := c.appendPingResult(logPath, "failure 0"); appendErr != nil {
						c.logger.Debugf("Failed to append ping result for namespace %s: %v", namespace, appendErr)
					}
				}
			}
		}()

		go io.Copy(io.Discard, stderr)

		if err := cmd.Wait(); err != nil {
			c.logger.Debugf("Ping process completed for namespace %s (some pings may have failed)", namespace)
		}

		timeoutCount := expectedPings - receivedPings
		for i := 0; i < timeoutCount; i++ {
			if appendErr := c.appendPingResult(logPath, "failure 0"); appendErr != nil {
				c.logger.Debugf("Failed to append timeout result for namespace %s: %v", namespace, appendErr)
			}
		}
	}()
}

// extractLatencyFromPingOutput parses the ping command output to extract latency in milliseconds
func extractLatencyFromPingOutput(output string) float64 {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "time=") {
			parts := strings.Split(line, "time=")
			if len(parts) > 1 {
				latencyPart := strings.Fields(parts[1])[0]
				latencyStr := strings.TrimSuffix(latencyPart, "ms")
				latency, err := strconv.ParseFloat(latencyStr, 64)
				if err == nil {
					return latency
				}
			}
		}
	}
	return 0
}
