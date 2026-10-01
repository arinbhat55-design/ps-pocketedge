/// One entry of the audit trail — GET /api/audit-events (admin). Covers
/// every user-initiated change across Compose files, variable groups,
/// deployments, approvals, environment policies, and Git repositories.
class AuditEvent {
  final int id;
  final String? actorEmail;

  /// Made through the no-login local session, which borrows an admin account.
  final bool localSession;
  final String action;
  final String entityType;
  final String entityId;
  final String summary;
  final Map<String, dynamic> details;
  final DateTime createdAt;

  const AuditEvent({
    required this.id,
    this.actorEmail,
    this.localSession = false,
    required this.action,
    required this.entityType,
    this.entityId = '',
    this.summary = '',
    this.details = const {},
    required this.createdAt,
  });

  /// Who did it — "system" for webhooks, the scheduler, and auto-rollback.
  String get actor => actorEmail == null
      ? 'system'
      : localSession
      ? '$actorEmail (local session)'
      : actorEmail!;

  factory AuditEvent.fromJson(Map<String, dynamic> json) {
    return AuditEvent(
      id: (json['id'] as num).toInt(),
      actorEmail: json['actorEmail'] as String?,
      localSession: json['localSession'] as bool? ?? false,
      action: json['action'] as String,
      entityType: json['entityType'] as String,
      entityId: json['entityId'] as String? ?? '',
      summary: json['summary'] as String? ?? '',
      details: json['details'] is Map<String, dynamic>
          ? json['details'] as Map<String, dynamic>
          : const {},
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
