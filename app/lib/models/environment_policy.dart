/// Weekday labels indexed the way the control plane stores them (UTC,
/// Sunday = 0).
const kWeekdays = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

/// A recurring weekly window (UTC) during which changes to an environment
/// may run. An [end] at or before [start] wraps past midnight.
class MaintenanceWindow {
  final List<int> days;
  final String start;
  final String end;

  const MaintenanceWindow({
    required this.days,
    required this.start,
    required this.end,
  });

  factory MaintenanceWindow.fromJson(Map<String, dynamic> json) {
    return MaintenanceWindow(
      days: (json['days'] as List<dynamic>? ?? [])
          .map((e) => (e as num).toInt())
          .toList(),
      start: json['start'] as String? ?? '00:00',
      end: json['end'] as String? ?? '00:00',
    );
  }

  Map<String, dynamic> toJson() => {'days': days, 'start': start, 'end': end};

  String describe() {
    final sorted = [...days]..sort();
    return '${sorted.map((d) => kWeekdays[d]).join(', ')} $start–$end UTC';
  }
}

/// One environment's governance rules — GET/PUT
/// /api/environment-policies.
class EnvironmentPolicy {
  final String environment;
  final bool requireApproval;
  final bool allowSelfApproval;
  final bool requireChangeRequest;
  final bool requireRollbackPlan;
  final bool enforceMaintenanceWindow;
  final List<MaintenanceWindow> maintenanceWindows;
  // Only on the list endpoint.
  final bool inMaintenanceWindow;
  final DateTime? nextWindow;

  const EnvironmentPolicy({
    required this.environment,
    this.requireApproval = false,
    this.allowSelfApproval = true,
    this.requireChangeRequest = false,
    this.requireRollbackPlan = false,
    this.enforceMaintenanceWindow = false,
    this.maintenanceWindows = const [],
    this.inMaintenanceWindow = false,
    this.nextWindow,
  });

  factory EnvironmentPolicy.fromJson(Map<String, dynamic> json) {
    return EnvironmentPolicy(
      environment: json['environment'] as String,
      requireApproval: json['requireApproval'] as bool? ?? false,
      allowSelfApproval: json['allowSelfApproval'] as bool? ?? true,
      requireChangeRequest: json['requireChangeRequest'] as bool? ?? false,
      requireRollbackPlan: json['requireRollbackPlan'] as bool? ?? false,
      enforceMaintenanceWindow:
          json['enforceMaintenanceWindow'] as bool? ?? false,
      maintenanceWindows: (json['maintenanceWindows'] as List<dynamic>? ?? [])
          .map((e) => MaintenanceWindow.fromJson(e as Map<String, dynamic>))
          .toList(),
      inMaintenanceWindow: json['inMaintenanceWindow'] as bool? ?? false,
      nextWindow: json['nextWindow'] == null
          ? null
          : DateTime.parse(json['nextWindow'] as String),
    );
  }

  Map<String, dynamic> toJson() => {
    'requireApproval': requireApproval,
    'allowSelfApproval': allowSelfApproval,
    'requireChangeRequest': requireChangeRequest,
    'requireRollbackPlan': requireRollbackPlan,
    'enforceMaintenanceWindow': enforceMaintenanceWindow,
    'maintenanceWindows': [for (final w in maintenanceWindows) w.toJson()],
  };

  /// Short labels for the rules in force, for chips/summaries.
  List<String> get ruleLabels => [
    if (requireApproval)
      allowSelfApproval ? 'Approval required' : 'Approval by another admin',
    if (requireChangeRequest) 'Change request required',
    if (requireRollbackPlan) 'Rollback plan required',
    if (enforceMaintenanceWindow) 'Maintenance window',
  ];
}
