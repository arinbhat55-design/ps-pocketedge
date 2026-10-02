/// One service compose-go resolved out of a compose file (after env-var
/// substitution) — what POST /api/deployments/preview reports would be
/// created, without actually creating anything. nanoCpus/memoryLimitBytes
/// are 0 when the service declares no deploy.resources limit.
class DeploymentPreviewService {
  final String name;
  final String image;
  final List<String> ports;
  final List<String> volumes;
  final int environmentCount;
  final int nanoCpus;
  final int memoryLimitBytes;

  const DeploymentPreviewService({
    required this.name,
    required this.image,
    this.ports = const [],
    this.volumes = const [],
    this.environmentCount = 0,
    this.nanoCpus = 0,
    this.memoryLimitBytes = 0,
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
      nanoCpus: (json['nanoCpus'] as num?)?.toInt() ?? 0,
      memoryLimitBytes: (json['memoryLimitBytes'] as num?)?.toInt() ?? 0,
    );
  }
}

/// "Confirm sufficient CPU, RAM, and storage" + "Show estimated resource
/// consumption" + a simple "Generate deployment risk score" — present only
/// when the preview request named a target server that has reported host
/// capacity (see the control plane's deploymentResourceCheck doc comment;
/// an older agent or one that hasn't heartbeated yet omits this whole
/// section rather than comparing against a false zero).
class DeploymentResourceCheck {
  final int requestedNanoCpus;
  final int requestedMemoryBytes;
  final int serverTotalCpus;
  final int serverTotalMemoryBytes;
  final int serverAvailableMemoryBytes;
  final double serverAvailableCpuCores;
  final double serverDiskPercentUsed;
  final bool sufficientMemory;
  final bool sufficientCpu;
  final String riskScore; // "low" | "medium" | "high"

  const DeploymentResourceCheck({
    this.requestedNanoCpus = 0,
    this.requestedMemoryBytes = 0,
    this.serverTotalCpus = 0,
    this.serverTotalMemoryBytes = 0,
    this.serverAvailableMemoryBytes = 0,
    this.serverAvailableCpuCores = 0,
    this.serverDiskPercentUsed = 0,
    this.sufficientMemory = true,
    this.sufficientCpu = true,
    this.riskScore = 'low',
  });

  factory DeploymentResourceCheck.fromJson(Map<String, dynamic> json) {
    return DeploymentResourceCheck(
      requestedNanoCpus: (json['requestedNanoCpus'] as num?)?.toInt() ?? 0,
      requestedMemoryBytes:
          (json['requestedMemoryBytes'] as num?)?.toInt() ?? 0,
      serverTotalCpus: (json['serverTotalCpus'] as num?)?.toInt() ?? 0,
      serverTotalMemoryBytes:
          (json['serverTotalMemoryBytes'] as num?)?.toInt() ?? 0,
      serverAvailableMemoryBytes:
          (json['serverAvailableMemoryBytes'] as num?)?.toInt() ?? 0,
      serverAvailableCpuCores:
          (json['serverAvailableCpuCores'] as num?)?.toDouble() ?? 0,
      serverDiskPercentUsed:
          (json['serverDiskPercentUsed'] as num?)?.toDouble() ?? 0,
      sufficientMemory: json['sufficientMemory'] as bool? ?? true,
      sufficientCpu: json['sufficientCpu'] as bool? ?? true,
      riskScore: json['riskScore'] as String? ?? 'low',
    );
  }
}

/// "Check required ports": one service's published host port that's
/// already bound by another container on the target server.
class DeploymentPortConflict {
  final String service;
  final int hostPort;
  final String protocol;
  final String containerId;

  const DeploymentPortConflict({
    required this.service,
    required this.hostPort,
    required this.protocol,
    required this.containerId,
  });

  factory DeploymentPortConflict.fromJson(Map<String, dynamic> json) {
    return DeploymentPortConflict(
      service: json['service'] as String? ?? '',
      hostPort: (json['hostPort'] as num?)?.toInt() ?? 0,
      protocol: json['protocol'] as String? ?? 'tcp',
      containerId: json['containerId'] as String? ?? '',
    );
  }
}

/// "Check image availability" + "Check host architecture compatibility"
/// together, since both come from the same registry lookup. When
/// [available] is false, [archCompatible] is meaningless.
class DeploymentImageCheck {
  final String service;
  final String image;
  final bool available;
  final String error;
  final List<String> platforms;
  final bool archCompatible;

  const DeploymentImageCheck({
    required this.service,
    required this.image,
    this.available = false,
    this.error = '',
    this.platforms = const [],
    this.archCompatible = true,
  });

  factory DeploymentImageCheck.fromJson(Map<String, dynamic> json) {
    return DeploymentImageCheck(
      service: json['service'] as String? ?? '',
      image: json['image'] as String? ?? '',
      available: json['available'] as bool? ?? false,
      error: json['error'] as String? ?? '',
      platforms: (json['platforms'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      archCompatible: json['archCompatible'] as bool? ?? true,
    );
  }
}

/// "Validate volume paths": a service's named volume with an empty,
/// non-absolute, or duplicated mount target.
class DeploymentVolumeWarning {
  final String service;
  final String target;
  final String message;

  const DeploymentVolumeWarning({
    required this.service,
    required this.target,
    required this.message,
  });

  factory DeploymentVolumeWarning.fromJson(Map<String, dynamic> json) {
    return DeploymentVolumeWarning(
      service: json['service'] as String? ?? '',
      target: json['target'] as String? ?? '',
      message: json['message'] as String? ?? '',
    );
  }
}

class DeploymentPreview {
  final String name;
  final List<DeploymentPreviewService> services;
  final DeploymentResourceCheck? resourceCheck;
  final List<DeploymentPortConflict> portConflicts;
  final List<DeploymentImageCheck> imageChecks;
  final List<DeploymentVolumeWarning> volumeWarnings;
  final List<String> missingSecrets;
  final List<String> networkWarnings;

  const DeploymentPreview({
    required this.name,
    this.services = const [],
    this.resourceCheck,
    this.portConflicts = const [],
    this.imageChecks = const [],
    this.volumeWarnings = const [],
    this.missingSecrets = const [],
    this.networkWarnings = const [],
  });

  factory DeploymentPreview.fromJson(Map<String, dynamic> json) {
    return DeploymentPreview(
      name: json['name'] as String? ?? '',
      services: (json['services'] as List<dynamic>? ?? [])
          .map(
            (e) => DeploymentPreviewService.fromJson(e as Map<String, dynamic>),
          )
          .toList(),
      resourceCheck: json['resourceCheck'] == null
          ? null
          : DeploymentResourceCheck.fromJson(
              json['resourceCheck'] as Map<String, dynamic>,
            ),
      portConflicts: (json['portConflicts'] as List<dynamic>? ?? [])
          .map(
            (e) => DeploymentPortConflict.fromJson(e as Map<String, dynamic>),
          )
          .toList(),
      imageChecks: (json['imageChecks'] as List<dynamic>? ?? [])
          .map((e) => DeploymentImageCheck.fromJson(e as Map<String, dynamic>))
          .toList(),
      volumeWarnings: (json['volumeWarnings'] as List<dynamic>? ?? [])
          .map(
            (e) => DeploymentVolumeWarning.fromJson(e as Map<String, dynamic>),
          )
          .toList(),
      missingSecrets: (json['missingSecrets'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      networkWarnings: (json['networkWarnings'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
    );
  }
}
