/// One rollout actually dispatched to a server — GET
/// /api/deployments/{id}/revisions. The exact Compose content it ran is
/// only included when fetching a single revision.
class DeploymentRevision {
  final String id;
  final int revision;
  // deploy, redeploy, rollback
  final String action;
  final String composeContent;
  final Map<String, int> scales;
  final int? composeVersion;
  final String gitRef;
  final String gitCommit;
  final String strategy;
  // dispatched, running, failed, healthy, unhealthy, rolled_back
  final String status;
  final String statusMessage;
  final bool autoRollbackAttempted;
  final String? createdByEmail;
  final DateTime createdAt;

  const DeploymentRevision({
    required this.id,
    required this.revision,
    required this.action,
    this.composeContent = '',
    this.scales = const {},
    this.composeVersion,
    this.gitRef = '',
    this.gitCommit = '',
    this.strategy = 'recreate',
    required this.status,
    this.statusMessage = '',
    this.autoRollbackAttempted = false,
    this.createdByEmail,
    required this.createdAt,
  });

  /// Whether this revision could be rolled back to: it actually ran.
  bool get isRollbackCandidate =>
      status == 'healthy' || status == 'running' || status == 'unhealthy';

  factory DeploymentRevision.fromJson(Map<String, dynamic> json) {
    return DeploymentRevision(
      id: json['id'] as String? ?? '',
      revision: (json['revision'] as num).toInt(),
      action: json['action'] as String? ?? '',
      composeContent: json['composeContent'] as String? ?? '',
      scales: (json['scales'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, (v as num).toInt()),
      ),
      composeVersion: (json['composeVersion'] as num?)?.toInt(),
      gitRef: json['gitRef'] as String? ?? '',
      gitCommit: json['gitCommit'] as String? ?? '',
      strategy: json['strategy'] as String? ?? 'recreate',
      status: json['status'] as String? ?? '',
      statusMessage: json['statusMessage'] as String? ?? '',
      autoRollbackAttempted: json['autoRollbackAttempted'] as bool? ?? false,
      createdByEmail: json['createdByEmail'] as String?,
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
