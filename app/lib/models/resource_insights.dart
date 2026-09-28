/// Container resource insights and alerting. Mirrors the control plane's
/// insights package (internal/controlplane/insights) and store's
/// ContainerAlertRule/ContainerAlert (store/container_alerts.go).
library;

double _toDouble(Object? v) => (v as num?)?.toDouble() ?? 0;
int _toInt(Object? v) => (v as num?)?.toInt() ?? 0;
DateTime? _toDate(Object? v) => v == null ? null : DateTime.parse(v as String);

/// Human label for a metric name ("cpu", "memory", "pids").
String metricLabel(String metric) => switch (metric) {
  'cpu' => 'CPU',
  'memory' => 'Memory',
  'pids' => 'Processes',
  _ => metric,
};

/// Formats a container CPU value (the API's percent-of-one-core, so 150 =
/// 1.5 cores) in cores, the unit CPU limits are set in.
String formatCores(double cpuPercent) =>
    '${(cpuPercent / 100).toStringAsFixed(2)} cores';

/// Formats a metric value for display: CPU in cores, memory as a
/// percentage of its limit, processes as a count.
String formatMetricValue(String metric, double value) => switch (metric) {
  'pids' => value.toStringAsFixed(0),
  'cpu' => formatCores(value),
  _ => '${value.toStringAsFixed(1)}%',
};

/// One detected abnormal-usage pattern: a "spike" over the recent baseline,
/// or a memory "leak" (steady growth toward the limit).
class ResourceAnomaly {
  final String metric;
  final String kind;
  final String severity;
  final double current;
  final double baseline;
  final String message;

  const ResourceAnomaly({
    required this.metric,
    required this.kind,
    required this.severity,
    required this.current,
    required this.baseline,
    required this.message,
  });

  factory ResourceAnomaly.fromJson(Map<String, dynamic> json) =>
      ResourceAnomaly(
        metric: json['metric'] as String,
        kind: json['kind'] as String,
        severity: json['severity'] as String,
        current: _toDouble(json['current']),
        baseline: _toDouble(json['baseline']),
        message: json['message'] as String,
      );
}

/// One suggested limit change. [current]/[suggested] are in the same unit
/// as the matching [ResourceLimits] field (nano-CPUs, bytes, or a process
/// count); 0 means unlimited.
class ResourceRecommendation {
  final String resource; // cpu, memory, memoryReservation, pids
  final String action; // set, increase, decrease
  final String severity;
  final int current;
  final int suggested;
  final String message;

  const ResourceRecommendation({
    required this.resource,
    required this.action,
    required this.severity,
    required this.current,
    required this.suggested,
    required this.message,
  });

  factory ResourceRecommendation.fromJson(Map<String, dynamic> json) =>
      ResourceRecommendation(
        resource: json['resource'] as String,
        action: json['action'] as String,
        severity: json['severity'] as String,
        current: _toInt(json['current']),
        suggested: _toInt(json['suggested']),
        message: json['message'] as String,
      );
}

/// A container's resource limits, as ContainerInspect reports them.
class ResourceLimits {
  final int nanoCpus;
  final int memoryLimitBytes;
  final int memoryReservationBytes;
  final int pidsLimit;

  const ResourceLimits({
    this.nanoCpus = 0,
    this.memoryLimitBytes = 0,
    this.memoryReservationBytes = 0,
    this.pidsLimit = 0,
  });

  factory ResourceLimits.fromJson(Map<String, dynamic> json) => ResourceLimits(
    nanoCpus: _toInt(json['nanoCpus']),
    memoryLimitBytes: _toInt(json['memoryLimitBytes']),
    memoryReservationBytes: _toInt(json['memoryReservationBytes']),
    pidsLimit: _toInt(json['pidsLimit']),
  );
}

/// Summary of the usage history the insights were computed from.
class UsageStats {
  final int sampleCount;
  final DateTime from;
  final DateTime to;
  final double cpuP50;
  final double cpuP95;
  final double cpuMax;
  final int memBytesP50;
  final int memBytesP95;
  final int memBytesMax;
  final int pidsMax;

  const UsageStats({
    required this.sampleCount,
    required this.from,
    required this.to,
    required this.cpuP50,
    required this.cpuP95,
    required this.cpuMax,
    required this.memBytesP50,
    required this.memBytesP95,
    required this.memBytesMax,
    required this.pidsMax,
  });

  factory UsageStats.fromJson(Map<String, dynamic> json) => UsageStats(
    sampleCount: _toInt(json['sampleCount']),
    from: DateTime.parse(json['from'] as String),
    to: DateTime.parse(json['to'] as String),
    cpuP50: _toDouble(json['cpuP50']),
    cpuP95: _toDouble(json['cpuP95']),
    cpuMax: _toDouble(json['cpuMax']),
    memBytesP50: _toInt(json['memBytesP50']),
    memBytesP95: _toInt(json['memBytesP95']),
    memBytesMax: _toInt(json['memBytesMax']),
    pidsMax: _toInt(json['pidsMax']),
  );
}

/// GET /api/servers/{id}/containers/{containerId}/insights.
class ContainerInsights {
  final UsageStats? stats;
  final List<ResourceAnomaly> anomalies;
  final List<ResourceRecommendation> recommendations;
  final ResourceLimits currentLimits;
  final ResourceLimits suggestedLimits;
  final List<ContainerAlert> openAlerts;

  const ContainerInsights({
    this.stats,
    this.anomalies = const [],
    this.recommendations = const [],
    this.currentLimits = const ResourceLimits(),
    this.suggestedLimits = const ResourceLimits(),
    this.openAlerts = const [],
  });

  factory ContainerInsights.fromJson(Map<String, dynamic> json) =>
      ContainerInsights(
        stats: json['stats'] == null
            ? null
            : UsageStats.fromJson(json['stats'] as Map<String, dynamic>),
        anomalies: [
          for (final a in json['anomalies'] as List<dynamic>? ?? [])
            ResourceAnomaly.fromJson(a as Map<String, dynamic>),
        ],
        recommendations: [
          for (final r in json['recommendations'] as List<dynamic>? ?? [])
            ResourceRecommendation.fromJson(r as Map<String, dynamic>),
        ],
        currentLimits: ResourceLimits.fromJson(
          json['currentLimits'] as Map<String, dynamic>? ?? const {},
        ),
        suggestedLimits: ResourceLimits.fromJson(
          json['suggestedLimits'] as Map<String, dynamic>? ?? const {},
        ),
        openAlerts: [
          for (final a in json['openAlerts'] as List<dynamic>? ?? [])
            ContainerAlert.fromJson(a as Map<String, dynamic>),
        ],
      );
}

/// "Alert when [metric] stays above [threshold] for [durationSeconds]".
/// Null [serverId]/[containerName] mean every server / every container.
/// A CPU [threshold] is in the API's percent-of-one-core (150 = 1.5
/// cores); the UI converts to/from cores at the edges.
class ContainerAlertRule {
  final String id;
  final String name;
  final String? serverId;
  final String? containerName;
  final String metric;
  final double threshold;
  final int durationSeconds;
  final String severity;
  final bool enabled;

  const ContainerAlertRule({
    required this.id,
    required this.name,
    this.serverId,
    this.containerName,
    required this.metric,
    required this.threshold,
    required this.durationSeconds,
    required this.severity,
    required this.enabled,
  });

  factory ContainerAlertRule.fromJson(Map<String, dynamic> json) =>
      ContainerAlertRule(
        id: json['id'] as String,
        name: json['name'] as String,
        serverId: json['serverId'] as String?,
        containerName: json['containerName'] as String?,
        metric: json['metric'] as String,
        threshold: _toDouble(json['threshold']),
        durationSeconds: _toInt(json['durationSeconds']),
        severity: json['severity'] as String,
        enabled: json['enabled'] as bool? ?? true,
      );

  Map<String, dynamic> toJson() => {
    'name': name,
    'serverId': serverId,
    'containerName': containerName,
    'metric': metric,
    'threshold': threshold,
    'durationSeconds': durationSeconds,
    'severity': severity,
    'enabled': enabled,
  };

  ContainerAlertRule copyWith({bool? enabled}) => ContainerAlertRule(
    id: id,
    name: name,
    serverId: serverId,
    containerName: containerName,
    metric: metric,
    threshold: threshold,
    durationSeconds: durationSeconds,
    severity: severity,
    enabled: enabled ?? this.enabled,
  );

  /// e.g. "CPU > 90.0% for 5 min".
  String get conditionLabel {
    final base =
        '${metricLabel(metric)} > ${formatMetricValue(metric, threshold)}';
    if (durationSeconds == 0) return base;
    final d = Duration(seconds: durationSeconds);
    final span = d.inMinutes >= 1 ? '${d.inMinutes} min' : '${d.inSeconds} s';
    return '$base for $span';
  }
}

/// One firing ([isOpen]) or resolved container alert — raised by a rule
/// (kind "threshold") or by abnormal-usage detection (kind "anomaly").
class ContainerAlert {
  final String id;
  final String kind;
  final String? ruleId;
  final String? ruleName;
  final String? anomalyKind;
  final String serverId;
  final String serverName;
  final String containerId;
  final String containerName;
  final String metric;
  final String severity;
  final double? threshold;
  final double value;
  final String message;
  final DateTime startedAt;
  final DateTime lastSeenAt;
  final DateTime? resolvedAt;
  final DateTime? acknowledgedAt;

  const ContainerAlert({
    required this.id,
    required this.kind,
    this.ruleId,
    this.ruleName,
    this.anomalyKind,
    required this.serverId,
    required this.serverName,
    required this.containerId,
    required this.containerName,
    required this.metric,
    required this.severity,
    this.threshold,
    required this.value,
    required this.message,
    required this.startedAt,
    required this.lastSeenAt,
    this.resolvedAt,
    this.acknowledgedAt,
  });

  bool get isOpen => resolvedAt == null;
  bool get isAcknowledged => acknowledgedAt != null;

  factory ContainerAlert.fromJson(Map<String, dynamic> json) => ContainerAlert(
    id: json['id'] as String,
    kind: json['kind'] as String,
    ruleId: json['ruleId'] as String?,
    ruleName: json['ruleName'] as String?,
    anomalyKind: json['anomalyKind'] as String?,
    serverId: json['serverId'] as String,
    serverName: json['serverName'] as String? ?? '',
    containerId: json['containerId'] as String,
    containerName: json['containerName'] as String,
    metric: json['metric'] as String,
    severity: json['severity'] as String,
    threshold: json['threshold'] == null ? null : _toDouble(json['threshold']),
    value: _toDouble(json['value']),
    message: json['message'] as String,
    startedAt: DateTime.parse(json['startedAt'] as String),
    lastSeenAt: DateTime.parse(json['lastSeenAt'] as String),
    resolvedAt: _toDate(json['resolvedAt']),
    acknowledgedAt: _toDate(json['acknowledgedAt']),
  );
}
