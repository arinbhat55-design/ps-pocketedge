class ImageBuild {
  final String id;
  final String deploymentId;
  final String service;
  final int revision;
  final String status;
  final String statusMessage;
  final String imageTag;
  final String gitCommit;
  final String log;
  final bool reused;
  final DateTime createdAt;

  const ImageBuild({
    required this.id,
    required this.deploymentId,
    required this.service,
    required this.revision,
    required this.status,
    required this.statusMessage,
    required this.imageTag,
    required this.gitCommit,
    required this.log,
    required this.reused,
    required this.createdAt,
  });

  bool get isFinished =>
      const {'succeeded', 'failed', 'cancelled', 'superseded'}.contains(status);

  factory ImageBuild.fromJson(Map<String, dynamic> json) => ImageBuild(
    id: json['id'] as String,
    deploymentId: json['deploymentId'] as String? ?? '',
    service: json['service'] as String? ?? '',
    revision: (json['revision'] as num?)?.toInt() ?? 0,
    status: json['status'] as String? ?? 'queued',
    statusMessage: json['statusMessage'] as String? ?? '',
    imageTag: json['imageTag'] as String? ?? '',
    gitCommit: json['gitCommit'] as String? ?? '',
    log: json['log'] as String? ?? '',
    reused: json['reused'] as bool? ?? false,
    createdAt: DateTime.parse(json['createdAt'] as String),
  );
}
