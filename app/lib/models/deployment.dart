import 'deployment_event.dart';
import 'deployment_request.dart';
import 'deployment_revision.dart';
import 'environment_policy.dart';

/// GET /api/deployments/{id} — one deployment's governance, rollout, and
/// health state. The live status/progress timeline itself comes from the
/// deployment events WebSocket stream (see DeploymentStatusScreen).
class Deployment {
  final String id;
  final String? stackId;
  final String? composeFileId;
  final String serverId;
  final String phase;
  final String? deployEnvironment;
  final List<String> tags;
  final String changeRequest;
  final String notes;
  // "Rollback plan": free-text plan, plus automatic rollback to the last
  // healthy revision when post-deployment verification fails.
  final String rollbackPlan;
  final bool autoRollback;
  // Per-service replica overrides; a service not listed runs its
  // Compose-declared count.
  final Map<String, int> scales;
  // "recreate" or "rolling" (controlled update with automatic rollback).
  final String updateStrategy;
  // Branch/tag a Git-linked deployment tracks (empty = the Compose file's
  // own ref), and whether a push webhook redeploys it.
  final String gitRef;
  final bool autoDeploy;
  // Post-deployment health verification of the current revision:
  // unknown, verifying, healthy, or unhealthy.
  final String healthStatus;
  final String healthMessage;
  final DateTime? healthCheckedAt;
  final int currentRevision;
  final String? promotedFrom;
  final DateTime createdAt;
  final DateTime updatedAt;

  const Deployment({
    required this.id,
    this.stackId,
    this.composeFileId,
    required this.serverId,
    this.phase = 'pending',
    this.deployEnvironment,
    this.tags = const [],
    this.changeRequest = '',
    this.notes = '',
    this.rollbackPlan = '',
    this.autoRollback = false,
    this.scales = const {},
    this.updateStrategy = 'recreate',
    this.gitRef = '',
    this.autoDeploy = false,
    this.healthStatus = 'unknown',
    this.healthMessage = '',
    this.healthCheckedAt,
    this.currentRevision = 0,
    this.promotedFrom,
    required this.createdAt,
    required this.updatedAt,
  });

  factory Deployment.fromJson(Map<String, dynamic> json) {
    return Deployment(
      id: json['id'] as String,
      stackId: json['stackId'] as String?,
      composeFileId: json['composeFileId'] as String?,
      serverId: json['serverId'] as String? ?? '',
      phase: json['phase'] as String? ?? 'pending',
      deployEnvironment: json['deployEnvironment'] as String?,
      tags: (json['tags'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      changeRequest: json['changeRequest'] as String? ?? '',
      notes: json['notes'] as String? ?? '',
      rollbackPlan: json['rollbackPlan'] as String? ?? '',
      autoRollback: json['autoRollback'] as bool? ?? false,
      scales: (json['scales'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, (v as num).toInt()),
      ),
      updateStrategy: json['updateStrategy'] as String? ?? 'recreate',
      gitRef: json['gitRef'] as String? ?? '',
      autoDeploy: json['autoDeploy'] as bool? ?? false,
      healthStatus: json['healthStatus'] as String? ?? 'unknown',
      healthMessage: json['healthMessage'] as String? ?? '',
      healthCheckedAt: json['healthCheckedAt'] == null
          ? null
          : DateTime.parse(json['healthCheckedAt'] as String),
      currentRevision: (json['currentRevision'] as num?)?.toInt() ?? 0,
      promotedFrom: json['promotedFrom'] as String?,
      createdAt: DateTime.parse(json['createdAt'] as String),
      updatedAt: DateTime.parse(json['updatedAt'] as String),
    );
  }

  /// A copy with the editable metadata replaced — what the metadata dialog
  /// pops after a successful save.
  Deployment copyWithMetadata(DeploymentMetadata m) {
    return Deployment(
      id: id,
      stackId: stackId,
      composeFileId: composeFileId,
      serverId: serverId,
      phase: phase,
      deployEnvironment: m.environment,
      tags: m.tags,
      changeRequest: m.changeRequest,
      notes: m.notes,
      rollbackPlan: m.rollbackPlan,
      autoRollback: m.autoRollback,
      scales: scales,
      updateStrategy: m.updateStrategy,
      gitRef: m.gitRef,
      autoDeploy: m.autoDeploy,
      healthStatus: healthStatus,
      healthMessage: healthMessage,
      healthCheckedAt: healthCheckedAt,
      currentRevision: currentRevision,
      promotedFrom: promotedFrom,
      createdAt: createdAt,
      updatedAt: DateTime.now(),
    );
  }

  DeploymentMetadata get metadata => DeploymentMetadata(
    environment: deployEnvironment,
    tags: tags,
    changeRequest: changeRequest,
    notes: notes,
    rollbackPlan: rollbackPlan,
    autoRollback: autoRollback,
    updateStrategy: updateStrategy,
    gitRef: gitRef,
    autoDeploy: autoDeploy,
  );
}

/// The editable part of a deployment — PATCH /api/deployments/{id}/metadata
/// replaces all of it at once, so callers always send the full set.
class DeploymentMetadata {
  final String? environment;
  final List<String> tags;
  final String changeRequest;
  final String notes;
  final String rollbackPlan;
  final bool autoRollback;
  final String updateStrategy;
  final String gitRef;
  final bool autoDeploy;

  const DeploymentMetadata({
    this.environment,
    this.tags = const [],
    this.changeRequest = '',
    this.notes = '',
    this.rollbackPlan = '',
    this.autoRollback = false,
    this.updateStrategy = 'recreate',
    this.gitRef = '',
    this.autoDeploy = false,
  });

  Map<String, dynamic> toJson() => {
    if (environment != null) 'environment': environment,
    'tags': tags,
    'changeRequest': changeRequest,
    'notes': notes,
    'rollbackPlan': rollbackPlan,
    'autoRollback': autoRollback,
    'updateStrategy': updateStrategy,
    'gitRef': gitRef,
    'autoDeploy': autoDeploy,
  };
}

/// GET /api/deployments/{id} in full: the deployment plus its timeline,
/// open approval/scheduled requests, its environment's policy, and the
/// revision a rollback would currently go back to.
class DeploymentDetail {
  final Deployment deployment;
  final List<DeploymentEvent> events;
  final List<DeploymentRequest> openRequests;
  final EnvironmentPolicy? policy;
  final DeploymentRevision? rollbackTarget;
  final DeploymentRevision? currentRevision;
  final String sourceName;
  final List<String> serviceNames;

  const DeploymentDetail({
    required this.deployment,
    this.events = const [],
    this.openRequests = const [],
    this.policy,
    this.rollbackTarget,
    this.currentRevision,
    this.sourceName = '',
    this.serviceNames = const [],
  });

  factory DeploymentDetail.fromJson(Map<String, dynamic> json) {
    return DeploymentDetail(
      deployment: Deployment.fromJson(json),
      events: (json['events'] as List<dynamic>? ?? [])
          .map((e) => DeploymentEvent.fromJson(e as Map<String, dynamic>))
          .toList(),
      openRequests: (json['openRequests'] as List<dynamic>? ?? [])
          .map((e) => DeploymentRequest.fromJson(e as Map<String, dynamic>))
          .toList(),
      policy: json['policy'] == null
          ? null
          : EnvironmentPolicy.fromJson(json['policy'] as Map<String, dynamic>),
      rollbackTarget: json['rollbackTarget'] == null
          ? null
          : DeploymentRevision.fromJson(
              json['rollbackTarget'] as Map<String, dynamic>,
            ),
      currentRevision: json['currentRevisionDetail'] == null
          ? null
          : DeploymentRevision.fromJson(
              json['currentRevisionDetail'] as Map<String, dynamic>,
            ),
      sourceName: json['sourceName'] as String? ?? '',
      serviceNames: (json['serviceNames'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
    );
  }
}

/// One row of GET /api/deployments — "Deployment history".
class DeploymentSummary {
  final Deployment deployment;
  final String serverName;
  final String sourceName;
  final String? createdByEmail;
  final int pendingRequests;
  final String latestGitCommit;

  const DeploymentSummary({
    required this.deployment,
    this.serverName = '',
    this.sourceName = '',
    this.createdByEmail,
    this.pendingRequests = 0,
    this.latestGitCommit = '',
  });

  factory DeploymentSummary.fromJson(Map<String, dynamic> json) {
    return DeploymentSummary(
      deployment: Deployment.fromJson(json),
      serverName: json['serverName'] as String? ?? '',
      sourceName: json['sourceName'] as String? ?? '',
      createdByEmail: json['createdByEmail'] as String?,
      pendingRequests: (json['pendingRequests'] as num?)?.toInt() ?? 0,
      latestGitCommit: json['latestGitCommit'] as String? ?? '',
    );
  }
}

/// What a deploy/redeploy/rollback/scale/promote call produced: it was
/// dispatched (or, for scale/service redeploy, completed), or it's waiting
/// for approval or for the environment's next maintenance window.
class DeploymentActionOutcome {
  final String deploymentId;
  // dispatched, completed, pending_approval, scheduled
  final String status;
  final String? requestId;
  final DateTime? scheduledFor;
  final int revision;
  final String message;
  final bool? success;
  final String error;

  const DeploymentActionOutcome({
    required this.deploymentId,
    required this.status,
    this.requestId,
    this.scheduledFor,
    this.revision = 0,
    this.message = '',
    this.success,
    this.error = '',
  });

  bool get isQueued => status == 'pending_approval' || status == 'scheduled';

  factory DeploymentActionOutcome.fromJson(Map<String, dynamic> json) {
    return DeploymentActionOutcome(
      deploymentId: json['deploymentId'] as String? ?? '',
      status: json['status'] as String? ?? '',
      requestId: json['requestId'] as String?,
      scheduledFor: json['scheduledFor'] == null
          ? null
          : DateTime.parse(json['scheduledFor'] as String),
      revision: (json['revision'] as num?)?.toInt() ?? 0,
      message: json['message'] as String? ?? '',
      success: json['success'] as bool?,
      error: json['error'] as String? ?? '',
    );
  }

  /// A one-line description for a snackbar.
  String describe() {
    switch (status) {
      case 'pending_approval':
        return 'Waiting for approval — $message';
      case 'scheduled':
        final at = scheduledFor;
        return at == null
            ? 'Scheduled for the next maintenance window.'
            : 'Scheduled for the next maintenance window: ${formatTimestamp(at)}';
      case 'completed':
        return success == false ? 'Failed: $error' : message;
      default:
        return revision > 0
            ? 'Rollout started (revision $revision).'
            : (message.isEmpty ? 'Rollout started.' : message);
    }
  }
}

/// Governance choices when an action would be blocked by a maintenance
/// window.
class GateOptions {
  final bool overrideMaintenanceWindow;
  final bool scheduleForMaintenanceWindow;

  const GateOptions({
    this.overrideMaintenanceWindow = false,
    this.scheduleForMaintenanceWindow = false,
  });

  static const none = GateOptions();

  Map<String, dynamic> toJson() => {
    if (overrideMaintenanceWindow) 'overrideMaintenanceWindow': true,
    if (scheduleForMaintenanceWindow) 'scheduleForMaintenanceWindow': true,
  };
}

/// "2026-09-27 02:00" in local time — shared by the deployment screens.
String formatTimestamp(DateTime t) {
  final l = t.toLocal();
  String two(int n) => n.toString().padLeft(2, '0');
  return '${l.year}-${two(l.month)}-${two(l.day)} ${two(l.hour)}:${two(l.minute)}';
}

/// First 7 characters of a commit hash.
String shortCommit(String commit) =>
    commit.length > 7 ? commit.substring(0, 7) : commit;
