class Backup {
  final String id;
  final String deploymentId;
  final String status;
  final String message;
  final int? sizeBytes;
  final DateTime createdAt;
  final DateTime? completedAt;

  const Backup({
    required this.id,
    required this.deploymentId,
    required this.status,
    required this.message,
    required this.sizeBytes,
    required this.createdAt,
    required this.completedAt,
  });

  bool get isTerminal => status == 'completed' || status == 'failed';

  factory Backup.fromJson(Map<String, dynamic> json) {
    return Backup(
      id: json['id'] as String,
      deploymentId: json['deploymentId'] as String,
      status: json['status'] as String,
      message: json['message'] as String? ?? '',
      sizeBytes: json['sizeBytes'] as int?,
      createdAt: DateTime.parse(json['createdAt'] as String),
      completedAt: json['completedAt'] == null
          ? null
          : DateTime.parse(json['completedAt'] as String),
    );
  }
}
