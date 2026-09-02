/// One service compose-go resolved out of a compose file (after env-var
/// substitution) — what POST /api/deployments/preview reports would be
/// created, without actually creating anything.
class DeploymentPreviewService {
  final String name;
  final String image;
  final List<String> ports;
  final List<String> volumes;
  final int environmentCount;

  const DeploymentPreviewService({
    required this.name,
    required this.image,
    this.ports = const [],
    this.volumes = const [],
    this.environmentCount = 0,
  });

  factory DeploymentPreviewService.fromJson(Map<String, dynamic> json) {
    return DeploymentPreviewService(
      name: json['name'] as String? ?? '',
      image: json['image'] as String? ?? '',
      ports: (json['ports'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      volumes: (json['volumes'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      environmentCount: (json['environmentCount'] as num?)?.toInt() ?? 0,
    );
  }
}

class DeploymentPreview {
  final String name;
  final List<DeploymentPreviewService> services;

  const DeploymentPreview({required this.name, this.services = const []});

  factory DeploymentPreview.fromJson(Map<String, dynamic> json) {
    return DeploymentPreview(
      name: json['name'] as String? ?? '',
      services: (json['services'] as List<dynamic>? ?? [])
          .map(
            (e) =>
                DeploymentPreviewService.fromJson(e as Map<String, dynamic>),
          )
          .toList(),
    );
  }
}
