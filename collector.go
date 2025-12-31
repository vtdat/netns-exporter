package main

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
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
)

type Collector struct {
	logger          logrus.FieldLogger
	config          *NetnsExporterConfig
	nsMetrics       *prometheus.Desc
	intfMetrics     map[string]*prometheus.Desc
	dhcpMetrics     map[string]*prometheus.Desc
	qrouterMetrics  map[string]*prometheus.Desc
	ctMetrics       map[string]*prometheus.Desc
	snmpMetrics     map[string]*prometheus.Desc
	sockstatMetrics map[string]*prometheus.Desc
	hostname        string
}

func NewCollector(config *NetnsExporterConfig, logger *logrus.Logger) *Collector {
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

	return &Collector{
		logger:          logger.WithField("component", "collector"),
		config:          config,
		nsMetrics:       nsMetrics,
		intfMetrics:     intfMetrics,
		ctMetrics:       ctMetrics,
		snmpMetrics:     snmpMetrics,
		sockstatMetrics: sockstatMetrics,
		hostname:        hostname,
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
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	startTime := time.Now()

	// 1. Get Namespace Files
	nsFiles, err := os.ReadDir(NetnsPath)
	if err != nil {
		c.logger.Errorf("Failed to read namespace dir %s: %v", NetnsPath, err)
		return
	}

	ch <- prometheus.MustNewConstMetric(
		c.nsMetrics,
		prometheus.GaugeValue,
		float64(len(nsFiles)),
		c.hostname,
	)

	// 2. Setup Concurrency Control
	// We use the LimitedWaitGroup we defined earlier.
	wg := NewLimitedWaitGroup(c.config.Threads)

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
			c.collectNamespace(nsName, ch)
		}(name)
	}

	wg.Wait()
	c.logger.Debugf("Collection cycle took %s", time.Since(startTime))
}

// collectNamespace runs in a separate goroutine.
// It locks the OS thread, switches namespace, collects metrics, and cleans up.
func (c *Collector) collectNamespace(namespace string, ch chan<- prometheus.Metric) {
	// 1. Lock OS Thread (CRITICAL)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 2. Save Host Namespace (to restore later)
	originalNS, err := netns.Get()
	if err != nil {
		c.logger.Errorf("Failed to get current namespace: %v", err)
		return
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
		return
	}
	defer targetNS.Close()

	// 4. Switch to Target Namespace
	if err := netns.Set(targetNS); err != nil {
		c.logger.Errorf("Failed to switch to namespace %s: %v", namespace, err)
		return
	}

	// 5. Create Private Mount Namespace (Sandbox)
	// We need this ONLY for /sys. /proc is handled by thread-self.
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		c.logger.Errorf("Unshare failed for %s: %v", namespace, err)
		return
	}

	// Prevent mount propagation back to the host
	if err := syscall.Mount("", "/", "none", syscall.MS_SLAVE|syscall.MS_REC, ""); err != nil {
		c.logger.Errorf("Failed to remount root as slave in %s: %v", namespace, err)
		return
	}

	// 6. Mount /sys
	// We MUST still mount /sys because /sys/class/net is not thread-aware.
	// A. Unmount old /sys (best effort)
	_ = syscall.Unmount("/sys", syscall.MNT_DETACH)

	// B. Mount new /sys specific to this namespace
	if err := syscall.Mount("sysfs", "/sys", "sysfs", syscall.MS_RDONLY, ""); err != nil {
		c.logger.Warnf("Failed to mount sysfs for %s: %v", namespace, err)
		return
	}
	defer syscall.Unmount("/sys", syscall.MNT_DETACH)

	// 7. Collect Metrics
	// Interfaces use /sys (mounted above)
	c.collectInterfaces(namespace, ch)

	// These now use /proc/thread-self/..., so they automatically see the new NS
	c.collectCtMetrics(namespace, ch)
	c.collectSnmpMetrics(namespace, ch)
	c.collectSockstatMetrics(namespace, ch)
}

func (c *Collector) collectInterfaces(namespace string, ch chan<- prometheus.Metric) {
	ifFiles, err := os.ReadDir(InterfaceStatPath)
	if err != nil {
		c.logger.Errorf("Failed to read %s in namespace %s: %v", InterfaceStatPath, namespace, err)
		return
	}

	for _, ifFile := range ifFiles {
		devName := ifFile.Name()

		// Skip loopback and filtered devices
		if devName == "lo" || !c.config.DeviceFilter.IsAllowed(devName) {
			continue
		}

		// Get IP (Note: We are already IN the namespace, so we just use net package)
		deviceIP, err := c.getIPv4Address(devName)
		if err != nil {
			c.logger.Debugf("Could not get IP for %s in %s: %v", devName, namespace, err)
		}

		// Read metrics
		for metricName, desc := range c.intfMetrics {
			val := c.readFloatFromFile(InterfaceStatPath + devName + "/statistics/" + metricName)
			if val == -1 {
				continue
			}

			ch <- prometheus.MustNewConstMetric(
				desc,
				prometheus.CounterValue,
				val,
				namespace,
				devName,
				getType(namespace),
				c.hostname,
				deviceIP,
			)
		}
	}
}

func (c *Collector) collectCtMetrics(namespace string, ch chan<- prometheus.Metric) {
	metricPaths := map[string]string{
		"Conntrack_Total": conntrackTotalPath,
		"Conntrack_Max":   conntrackMaxPath,
	}
	for metricName, metricPath := range metricPaths {
		desc := c.ctMetrics[metricName]
		val := c.readFloatFromFile(ProcStatPath + metricPath)
		if val == -1 {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			desc,
			prometheus.GaugeValue,
			val,
			namespace,
			getType(namespace),
			c.hostname,
		)
	}
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
func (c *Collector) collectSnmpMetrics(namespace string, ch chan<- prometheus.Metric) {
	snmpFile := ProcThreadStatPath + "net/snmp"
	data, err := os.ReadFile(snmpFile)
	if err != nil {
		c.logger.Errorf("Failed to read SNMP file %s in namespace %s: %v", snmpFile, namespace, err)
		return
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

	// Map metrics to descriptors and emit
	metricMap := map[string]struct {
		proto string
		field string
		desc  *prometheus.Desc
	}{
		"Tcp_RetransSegs": {"Tcp", "RetransSegs", c.snmpMetrics["Tcp_RetransSegs"]},
		"Tcp_InErrs":      {"Tcp", "InErrs", c.snmpMetrics["Tcp_InErrs"]},
		"Udp_InErrors":    {"Udp", "InErrors", c.snmpMetrics["Udp_InErrors"]},
		"Udp_NoPorts":     {"Udp", "NoPorts", c.snmpMetrics["Udp_NoPorts"]},
	}

	for _, info := range metricMap {
		if val, ok := snmpData[info.proto][info.field]; ok {
			ch <- prometheus.MustNewConstMetric(
				info.desc,
				prometheus.CounterValue,
				val,
				namespace,
				getType(namespace),
				c.hostname,
			)
		}
	}
}

// collectSockstatMetrics collects socket statistics from /proc/net/sockstat
func (c *Collector) collectSockstatMetrics(namespace string, ch chan<- prometheus.Metric) {
	sockstatFile := ProcThreadStatPath + "net/sockstat"
	data, err := os.ReadFile(sockstatFile)
	if err != nil {
		c.logger.Errorf("Failed to read sockstat file %s in namespace %s: %v", sockstatFile, namespace, err)
		return
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

	// Map metrics to descriptors and emit
	metricMap := map[string]struct {
		proto string
		field string
		desc  *prometheus.Desc
	}{
		"sockets_used": {"sockets", "used", c.sockstatMetrics["sockets_used"]},
		"TCP_inuse":    {"TCP", "inuse", c.sockstatMetrics["TCP_inuse"]},
		"TCP_tw":       {"TCP", "tw", c.sockstatMetrics["TCP_tw"]},
		"TCP_mem":      {"TCP", "mem", c.sockstatMetrics["TCP_mem"]},
		"UDP_inuse":    {"UDP", "inuse", c.sockstatMetrics["UDP_inuse"]},
	}

	for _, info := range metricMap {
		if val, ok := sockstatData[info.proto][info.field]; ok {
			ch <- prometheus.MustNewConstMetric(
				info.desc,
				prometheus.GaugeValue,
				val,
				namespace,
				getType(namespace),
				c.hostname,
			)
		}
	}
}

// getIPv4Address retrieves the first IPv4 address of the interface.
// Assumes the current thread is already in the correct namespace.
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
		// ParseCIDR handles both "192.168.1.1/24" and "::1/128"
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}

		// Check if it's strictly IPv4 (To4 returns non-nil)
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
	} else if strings.HasPrefix(namespace, "dhcp-") {
		return "dhcp"
	}
	return "other"
}
