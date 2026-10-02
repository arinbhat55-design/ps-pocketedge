/// A change to a deployment waiting for approval or for its environment's
/// next maintenance window — GET /api/deployment-requests. "Approval
/// workflow" + "Maintenance window".
class DeploymentRequest {
  final String id;
  final String deploymentId;
  // deploy, redeploy, rollback, scale, redeploy_service
  final String action;
  final Map<String, dynamic> params;
  // pending_approval, scheduled, executed, rejected, cancelled, failed
  final String status;
  final String reason;
  final String? requestedBy;
  final String? requestedByEmail;
  final DateTime requestedAt;
  final String? decidedByEmail;
  final DateTime? decidedAt;
  final String decisionComment;
  final DateTime? scheduledFor;
  final DateTime? executedAt;
  final String resultMessage;
  final String sourceName;
  final String serverName;
  final String? environment;
  final String changeRequest;

  const DeploymentRequest({
    required this.id,
    required this.deploymentId,
    required this.action,
    this.params = const {},
    required this.status,
    this.reason = '',
    this.requestedBy,
    this.requestedByEmail,
    required this.requestedAt,
    this.decidedByEmail,
    this.decidedAt,
    this.decisionComment = '',
    this.scheduledFor,
    this.executedAt,
    this.resultMessage = '',
    this.sourceName = '',
    this.serverName = '',
    this.environment,
    this.changeRequest = '',
  });

  bool get isOpen => status == 'pending_approval' || status == 'scheduled';

  /// A human summary of what's being requested.
  String get summary {
    switch (action) {
      case 'deploy':
        return params['fromDeploymentId'] != null
            ? 'Promote (revision ${params['fromRevision']})'
            : 'Initial deploy';
      case 'redeploy':
        return 'Redeploy';
      case 'rollback':
        if (params['revision'] != null) {
          return 'Roll back to revision ${params['revision']}';
        }
        if (params['gitCommit'] != null) {
          final c = params['gitCommit'] as String;
          return 'Roll back to commit ${c.length > 7 ? c.substring(0, 7) : c}';
        }
        return 'Roll back to an earlier Compose version';
      case 'scale':
        return 'Scale ${params['service']} to ${params['replicas']}';
      case 'redeploy_service':
        return 'Redeploy service ${params['service']}';
    }
    return action;
  }

  static DateTime? _date(Object? v) =>
      v == null ? null : DateTime.parse(v as String);

  factory DeploymentRequest.fromJson(Map<String, dynamic> json) {
    return DeploymentRequest(
      id: json['id'] as String,
      deploymentId: json['deploymentId'] as String,
      action: json['action'] as String,
      params: json['params'] as Map<String, dynamic>? ?? const {},
      status: json['status'] as String,
      reason: json['reason'] as String? ?? '',
      requestedBy: json['requestedBy'] as String?,
      requestedByEmail: json['requestedByEmail'] as String?,
      requestedAt: DateTime.parse(json['requestedAt'] as String),
      decidedByEmail: json['decidedByEmail'] as String?,
      decidedAt: _date(json['decidedAt']),
      decisionComment: json['decisionComment'] as String? ?? '',
      scheduledFor: _date(json['scheduledFor']),
      executedAt: _date(json['executedAt']),
      resultMessage: json['resultMessage'] as String? ?? '',
      sourceName: json['sourceName'] as String? ?? '',
      serverName: json['serverName'] as String? ?? '',
      environment: json['environment'] as String?,
      changeRequest: json['changeRequest'] as String? ?? '',
    );
  }
}
