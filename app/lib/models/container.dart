import 'package:flutter/material.dart';

import 'server_metrics.dart';

/// State-color convention shared by the per-server container list
/// (server_detail_screen.dart) and the fleet-wide one
/// (features/containers/container_list_screen.dart).
Color containerStateColor(String state) {
  switch (state) {
    case 'running':
      return Colors.green;
    case 'exited':
    case 'dead':
      return Colors.red;
    default:
      return Colors.grey;
  }
}

/// One row of GET /api/containers — a [ContainerInfo] plus fleet-wide
/// context (which server, which application/stack, who owns it, and its
/// grouping metadata) that a per-server view doesn't need.
class FleetContainer {
  final String serverId;
  final String serverName;
  final ContainerInfo container;
  final String? stackId;
  final String? stackName;
  final String? ownerId;
  final String? ownerEmail;
  final String? environment;
  final List<String> tags;

  const FleetContainer({
    required this.serverId,
    required this.serverName,
    required this.container,
    this.stackId,
    this.stackName,
    this.ownerId,
    this.ownerEmail,
    this.environment,
    this.tags = const [],
  });

  factory FleetContainer.fromJson(Map<String, dynamic> json) {
    return FleetContainer(
      serverId: json['serverId'] as String,
      serverName: json['serverName'] as String,
      container: ContainerInfo.fromJson(json),
      stackId: json['stackId'] as String?,
      stackName: json['stackName'] as String?,
      ownerId: json['ownerId'] as String?,
      ownerEmail: json['ownerEmail'] as String?,
      environment: json['environment'] as String?,
      tags: (json['tags'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
    );
  }
}

/// One entry in a container's bounded health-check result history (Docker
/// keeps its last 5 runs).
class HealthCheckEntry {
  final DateTime? start;
  final DateTime? end;
  final int exitCode;
  final String output;

  const HealthCheckEntry({
    this.start,
    this.end,
    required this.exitCode,
    required this.output,
  });

  factory HealthCheckEntry.fromJson(Map<String, dynamic> json) {
    DateTime? unixOrNull(dynamic v) {
      final n = (v as num?)?.toInt();
      if (n == null || n == 0) return null;
      return DateTime.fromMillisecondsSinceEpoch(n * 1000);
    }

    return HealthCheckEntry(
      start: unixOrNull(json['startUnix']),
      end: unixOrNull(json['endUnix']),
      exitCode: (json['exitCode'] as num?)?.toInt() ?? 0,
      output: json['output'] as String? ?? '',
    );
  }
}

/// Expensive per-container fields, fetched on demand from
/// POST /api/servers/{id}/containers/{containerId}/inspect.
class ContainerDetail {
  final String containerId;
  final List<String> env;
  final String restartPolicyName;
  final int restartPolicyMaxRetryCount;
  final String healthStatus;
  final int healthFailingStreak;
  final int restartCount;
  final List<HealthCheckEntry> healthLog;
  final List<String> command;
  final List<String> entrypoint;
  final String workingDir;
  final Map<String, String> labels;
  final String image;
  final int nanoCpus;
  final int memoryLimitBytes;
  final int memoryReservationBytes;
  final int pidsLimit;

  const ContainerDetail({
    required this.containerId,
    required this.env,
    required this.restartPolicyName,
    required this.restartPolicyMaxRetryCount,
    required this.healthStatus,
    required this.healthFailingStreak,
    required this.restartCount,
    this.healthLog = const [],
    this.command = const [],
    this.entrypoint = const [],
    this.workingDir = '',
    this.labels = const {},
    this.image = '',
    this.nanoCpus = 0,
    this.memoryLimitBytes = 0,
    this.memoryReservationBytes = 0,
    this.pidsLimit = 0,
  });

  factory ContainerDetail.fromJson(Map<String, dynamic> json) {
    return ContainerDetail(
      containerId: json['containerId'] as String,
      env: (json['env'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      restartPolicyName: json['restartPolicyName'] as String? ?? '',
      restartPolicyMaxRetryCount:
          (json['restartPolicyMaxRetryCount'] as num?)?.toInt() ?? 0,
      healthStatus: json['healthStatus'] as String? ?? '',
      healthFailingStreak: (json['healthFailingStreak'] as num?)?.toInt() ?? 0,
      restartCount: (json['restartCount'] as num?)?.toInt() ?? 0,
      healthLog: (json['healthLog'] as List<dynamic>? ?? [])
          .map((e) => HealthCheckEntry.fromJson(e as Map<String, dynamic>))
          .toList(),
      command: (json['command'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      entrypoint: (json['entrypoint'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
      workingDir: json['workingDir'] as String? ?? '',
      labels: (json['labels'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, v as String),
      ),
      image: json['image'] as String? ?? '',
      nanoCpus: (json['nanoCpus'] as num?)?.toInt() ?? 0,
      memoryLimitBytes: (json['memoryLimitBytes'] as num?)?.toInt() ?? 0,
      memoryReservationBytes:
          (json['memoryReservationBytes'] as num?)?.toInt() ?? 0,
      pidsLimit: (json['pidsLimit'] as num?)?.toInt() ?? 0,
    );
  }
}

/// One port mapping in a [ContainerConfig] request (create/recreate).
class ContainerPortSpec {
  final int containerPort;
  final int hostPort;
  final String protocol;

  const ContainerPortSpec({
    required this.containerPort,
    this.hostPort = 0,
    this.protocol = 'tcp',
  });

  Map<String, dynamic> toJson() => {
    'containerPort': containerPort,
    if (hostPort != 0) 'hostPort': hostPort,
    'protocol': protocol,
  };
}

/// One named-volume mount in a [ContainerConfig] request. Named volumes
/// only — same bind-mount rejection as the stack-deploy path.
class ContainerVolumeSpec {
  final String volumeName;
  final String target;
  final bool readOnly;

  const ContainerVolumeSpec({
    required this.volumeName,
    required this.target,
    this.readOnly = false,
  });

  Map<String, dynamic> toJson() => {
    'volumeName': volumeName,
    'target': target,
    if (readOnly) 'readOnly': readOnly,
  };
}

/// The full desired shape of a standalone container (not part of a
/// deployed stack) — the request body for creating or recreating one.
class ContainerConfig {
  final String image;
  final String name;
  final List<String> command;
  final List<String> env;
  final List<ContainerPortSpec> ports;
  final List<ContainerVolumeSpec> volumes;
  final String restartPolicyName;
  final int restartPolicyMaxRetryCount;
  final Map<String, String> labels;
  // Resource limits — 0 means "not set" (unlimited).
  final int nanoCpus;
  final int memoryLimitBytes;
  final int memoryReservationBytes;
  final int pidsLimit;

  const ContainerConfig({
    required this.image,
    required this.name,
    this.command = const [],
    this.env = const [],
    this.ports = const [],
    this.volumes = const [],
    this.restartPolicyName = 'no',
    this.restartPolicyMaxRetryCount = 0,
    this.labels = const {},
    this.nanoCpus = 0,
    this.memoryLimitBytes = 0,
    this.memoryReservationBytes = 0,
    this.pidsLimit = 0,
  });

  Map<String, dynamic> toJson() => {
    'image': image,
    'name': name,
    if (command.isNotEmpty) 'command': command,
    if (env.isNotEmpty) 'env': env,
    if (ports.isNotEmpty) 'ports': ports.map((p) => p.toJson()).toList(),
    if (volumes.isNotEmpty) 'volumes': volumes.map((v) => v.toJson()).toList(),
    'restartPolicyName': restartPolicyName,
    if (restartPolicyMaxRetryCount != 0)
      'restartPolicyMaxRetryCount': restartPolicyMaxRetryCount,
    if (labels.isNotEmpty) 'labels': labels,
    if (nanoCpus != 0) 'nanoCpus': nanoCpus,
    if (memoryLimitBytes != 0) 'memoryLimitBytes': memoryLimitBytes,
    if (memoryReservationBytes != 0)
      'memoryReservationBytes': memoryReservationBytes,
    if (pidsLimit != 0) 'pidsLimit': pidsLimit,
  };
}

/// Result of one container lifecycle command (action/create/rename/clone/
/// recreate/restart-policy update) that completed its round trip to the
/// agent.
class ContainerOpResult {
  final bool success;
  final String? error;
  final String? containerId;

  const ContainerOpResult({
    required this.success,
    this.error,
    this.containerId,
  });

  factory ContainerOpResult.fromJson(Map<String, dynamic> json) {
    return ContainerOpResult(
      success: json['success'] as bool? ?? false,
      error: json['error'] as String?,
      containerId: json['containerId'] as String?,
    );
  }
}

/// One {server, container} pair to act on within a bulk container action
/// request.
class BulkActionTarget {
  final String serverId;
  final String containerId;

  const BulkActionTarget({required this.serverId, required this.containerId});

  Map<String, dynamic> toJson() => {
    'serverId': serverId,
    'containerId': containerId,
  };
}

/// One target's outcome within a bulk container action.
class BulkActionResult {
  final String serverId;
  final String containerId;
  final bool success;
  final String? error;

  const BulkActionResult({
    required this.serverId,
    required this.containerId,
    required this.success,
    this.error,
  });

  factory BulkActionResult.fromJson(Map<String, dynamic> json) {
    return BulkActionResult(
      serverId: json['serverId'] as String,
      containerId: json['containerId'] as String,
      success: json['success'] as bool? ?? false,
      error: json['error'] as String?,
    );
  }
}
