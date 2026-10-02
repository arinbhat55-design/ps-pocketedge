/// One row of GET /api/networks — a Docker network present on a server,
/// with the owning server attached so a fleet-wide (no ?serverId filter)
/// list can show where each network lives.
class NetworkSummary {
  final String serverId;
  final String serverName;
  final String id;
  final String name;
  final String driver;
  final String scope;
  final bool internal;
  final Map<String, String> labels;
  final List<String> containerIds;

  const NetworkSummary({
    required this.serverId,
    required this.serverName,
    required this.id,
    required this.name,
    required this.driver,
    required this.scope,
    required this.internal,
    this.labels = const {},
    this.containerIds = const [],
  });

  factory NetworkSummary.fromJson(Map<String, dynamic> json) {
    return NetworkSummary(
      serverId: json['serverId'] as String? ?? '',
      serverName: json['serverName'] as String? ?? '',
      id: json['id'] as String? ?? '',
      name: json['name'] as String? ?? '',
      driver: json['driver'] as String? ?? '',
      scope: json['scope'] as String? ?? '',
      internal: json['internal'] as bool? ?? false,
      labels: (json['labels'] as Map<String, dynamic>? ?? {}).map(
        (k, v) => MapEntry(k, v as String),
      ),
      containerIds: (json['containerIds'] as List<dynamic>? ?? [])
          .map((e) => e as String)
          .toList(),
    );
  }
}

/// Result of a network create/remove/connect/disconnect command that
/// completed its round trip to the agent.
class NetworkOpResult {
  final bool success;
  final String? error;
  final String? networkId;

  const NetworkOpResult({required this.success, this.error, this.networkId});

  factory NetworkOpResult.fromJson(Map<String, dynamic> json) {
    return NetworkOpResult(
      success: json['success'] as bool? ?? false,
      error: json['error'] as String?,
      networkId: json['networkId'] as String?,
    );
  }
}
