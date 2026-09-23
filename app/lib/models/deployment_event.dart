/// Whole-stack phases during which a rollout is still in flight.
const _inProgressPhases = {'pending', 'pulling', 'creating', 'verifying'};

class DeploymentEvent {
  final int id;
  final String deploymentId;
  final String phase;
  final String message;
  // Set only for handler-initiated events (create/redeploy/rollback/stack
  // actions) — unset for agent-reported phase events, which have no human
  // actor. "Complete audit trail".
  final String? triggeredByEmail;
  // Set for a per-service progress event (one service's image pulled,
  // container started, ...); empty for whole-stack events.
  final String service;
  final DateTime createdAt;

  const DeploymentEvent({
    required this.id,
    required this.deploymentId,
    required this.phase,
    required this.message,
    this.triggeredByEmail,
    this.service = '',
    required this.createdAt,
  });

  bool get isServiceEvent => service.isNotEmpty;

  /// Whether this whole-stack event means a rollout is still running.
  /// Per-service events never count — the stack's own events do.
  bool get isInProgress => !isServiceEvent && _inProgressPhases.contains(phase);

  /// Whether this whole-stack event leaves the deployment settled (running,
  /// failed, verified, rolled back, waiting on approval, ...), so actions
  /// like redeploy make sense again.
  bool get isTerminal => !isServiceEvent && !isInProgress;

  factory DeploymentEvent.fromJson(Map<String, dynamic> json) {
    return DeploymentEvent(
      id: json['id'] as int,
      deploymentId: json['deploymentId'] as String,
      phase: json['phase'] as String,
      message: json['message'] as String? ?? '',
      triggeredByEmail: json['triggeredByEmail'] as String?,
      service: json['service'] as String? ?? '',
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}

/// One service's progress within the most recent rollout: its latest
/// per-service event since the rollout started.
class ServiceProgress {
  final String service;
  final String phase;
  final String message;

  const ServiceProgress({
    required this.service,
    required this.phase,
    required this.message,
  });
}

/// Per-service progress of the latest rollout in [events] — "Show
/// deployment progress by service". A rollout starts at the last
/// whole-stack "pending" event; services appear in the order they were
/// first reported (dependency order).
List<ServiceProgress> latestServiceProgress(List<DeploymentEvent> events) {
  var start = 0;
  for (var i = events.length - 1; i >= 0; i--) {
    final e = events[i];
    if (!e.isServiceEvent && e.phase == 'pending') {
      start = i;
      break;
    }
  }
  final latest = <String, ServiceProgress>{};
  for (final e in events.skip(start)) {
    if (!e.isServiceEvent) continue;
    latest[e.service] = ServiceProgress(
      service: e.service,
      phase: e.phase,
      message: e.message,
    );
  }
  return latest.values.toList();
}
