class DeploymentEvent {
  final int id;
  final String deploymentId;
  final String phase;
  final String message;
  final DateTime createdAt;

  const DeploymentEvent({
    required this.id,
    required this.deploymentId,
    required this.phase,
    required this.message,
    required this.createdAt,
  });

  bool get isTerminal => phase == 'running' || phase == 'failed';

  factory DeploymentEvent.fromJson(Map<String, dynamic> json) {
    return DeploymentEvent(
      id: json['id'] as int,
      deploymentId: json['deploymentId'] as String,
      phase: json['phase'] as String,
      message: json['message'] as String? ?? '',
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
