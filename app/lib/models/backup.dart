class Backup {
  final String id;
  final String deploymentId;
  final String status;
  final String message;

  /// manual or scheduled.
  final String origin;
  final bool quiesced;
  final String format;
  final int? sizeBytes;
  final DateTime createdAt;
  final DateTime? completedAt;

  const Backup({
    required this.id,
    required this.deploymentId,
    required this.status,
    required this.message,
    this.origin = 'manual',
    this.quiesced = false,
    this.format = 'volumes',
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
      origin: json['origin'] as String? ?? 'manual',
      quiesced: json['quiesced'] as bool? ?? false,
      format: json['format'] as String? ?? 'volumes',
      sizeBytes: json['sizeBytes'] as int?,
      createdAt: DateTime.parse(json['createdAt'] as String),
      completedAt: json['completedAt'] == null
          ? null
          : DateTime.parse(json['completedAt'] as String),
    );
  }
}
