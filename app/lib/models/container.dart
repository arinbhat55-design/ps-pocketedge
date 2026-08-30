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

  const ContainerDetail({
    required this.containerId,
    required this.env,
    required this.restartPolicyName,
    required this.restartPolicyMaxRetryCount,
    required this.healthStatus,
    required this.healthFailingStreak,
    required this.restartCount,
  });

  factory ContainerDetail.fromJson(Map<String, dynamic> json) {
    return ContainerDetail(
      containerId: json['containerId'] as String,
      env: (json['env'] as List<dynamic>? ?? []).map((e) => e as String).toList(),
      restartPolicyName: json['restartPolicyName'] as String? ?? '',
      restartPolicyMaxRetryCount:
          (json['restartPolicyMaxRetryCount'] as num?)?.toInt() ?? 0,
      healthStatus: json['healthStatus'] as String? ?? '',
      healthFailingStreak: (json['healthFailingStreak'] as num?)?.toInt() ?? 0,
      restartCount: (json['restartCount'] as num?)?.toInt() ?? 0,
    );
  }
}
