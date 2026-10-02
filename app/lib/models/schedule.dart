/// A recurring (cron) or one-time container start/stop schedule. Mirrors
/// store.Schedule (internal/controlplane/store/schedules.go). Everything
/// timestamp-shaped here is UTC; the UI converts to/from the viewer's
/// local time zone at the edges.
class Schedule {
  final String id;
  final String serverId;
  final String containerId;
  final String containerName;
  final String action; // "start" or "stop"
  final String scheduleType; // "recurring" or "once"
  final String? cronExpr;
  final DateTime? runOnceAt;
  final DateTime nextRunAt;
  final DateTime? lastRunAt;
  final String? lastRunStatus;
  final bool enabled;

  const Schedule({
    required this.id,
    required this.serverId,
    required this.containerId,
    required this.containerName,
    required this.action,
    required this.scheduleType,
    this.cronExpr,
    this.runOnceAt,
    required this.nextRunAt,
    this.lastRunAt,
    this.lastRunStatus,
    required this.enabled,
  });

  factory Schedule.fromJson(Map<String, dynamic> json) {
    return Schedule(
      id: json['id'] as String,
      serverId: json['serverId'] as String,
      containerId: json['containerId'] as String,
      containerName: json['containerName'] as String,
      action: json['action'] as String,
      scheduleType: json['scheduleType'] as String,
      cronExpr: json['cronExpr'] as String?,
      runOnceAt: json['runOnceAt'] == null
          ? null
          : DateTime.parse(json['runOnceAt'] as String),
      nextRunAt: DateTime.parse(json['nextRunAt'] as String),
      lastRunAt: json['lastRunAt'] == null
          ? null
          : DateTime.parse(json['lastRunAt'] as String),
      lastRunStatus: json['lastRunStatus'] as String?,
      enabled: json['enabled'] as bool? ?? true,
    );
  }
}
