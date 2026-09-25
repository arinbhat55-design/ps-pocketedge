import 'backup.dart';

/// One offered image tag of a [DatabaseEngine].
class EngineVersion {
  final String tag;
  final String label;

  const EngineVersion({required this.tag, required this.label});

  factory EngineVersion.fromJson(Map<String, dynamic> json) => EngineVersion(
    tag: json['tag'] as String,
    label: json['label'] as String? ?? json['tag'] as String,
  );
}

/// An extra port as published for one instance (see
/// dbcatalog.Engine.ExtraHostPorts).
class PublishedPort {
  final String name;
  final int container;
  final int host;

  const PublishedPort({
    required this.name,
    required this.container,
    required this.host,
  });

  factory PublishedPort.fromJson(Map<String, dynamic> json) => PublishedPort(
    name: json['name'] as String? ?? '',
    container: json['container'] as int,
    host: json['host'] as int,
  );

  static List<PublishedPort> listFrom(dynamic json) =>
      (json as List<dynamic>? ?? [])
          .map((e) => PublishedPort.fromJson(e as Map<String, dynamic>))
          .toList();
}

class EnginePort {
  final int container;
  final String name;

  const EnginePort({required this.container, required this.name});

  factory EnginePort.fromJson(Map<String, dynamic> json) => EnginePort(
    container: json['container'] as int,
    name: json['name'] as String? ?? '',
  );
}

/// A curated Database Marketplace entry (GET /api/database-engines).
class DatabaseEngine {
  final String id;
  final String name;
  final String category;
  final String description;
  final String image;
  final List<EngineVersion> versions;
  final EnginePort port;
  final List<EnginePort> extraPorts;

  /// password, token, or none.
  final String auth;
  final String? fixedUsername;
  final bool usernameAllowed;
  final String? defaultUsername;
  final bool databaseNameAllowed;
  final String? databaseNameLabel;
  final List<String> architectures;
  final bool persistent;
  final int minMemoryMb;
  final int defaultMemoryMb;
  final double defaultCpus;
  final int defaultStorageGb;

  /// What the HA option deploys; null when the engine has none.
  final String? highAvailability;

  /// exec, redeploy, or none.
  final String rotation;
  final bool temporaryUsers;
  final String license;
  final bool requiresLicenseAcceptance;
  final List<String> editions;
  final List<String> notes;

  const DatabaseEngine({
    required this.id,
    required this.name,
    required this.category,
    required this.description,
    required this.image,
    required this.versions,
    required this.port,
    required this.extraPorts,
    required this.auth,
    required this.fixedUsername,
    required this.usernameAllowed,
    required this.defaultUsername,
    required this.databaseNameAllowed,
    required this.databaseNameLabel,
    required this.architectures,
    required this.persistent,
    required this.minMemoryMb,
    required this.defaultMemoryMb,
    required this.defaultCpus,
    required this.defaultStorageGb,
    required this.highAvailability,
    required this.rotation,
    required this.temporaryUsers,
    required this.license,
    required this.requiresLicenseAcceptance,
    required this.editions,
    required this.notes,
  });

  bool get hasCredentials => auth != 'none';
  bool get usesPassword => auth == 'password';
  bool get supportsHighAvailability => highAvailability != null;

  factory DatabaseEngine.fromJson(Map<String, dynamic> json) {
    List<String> strings(String key) =>
        (json[key] as List<dynamic>? ?? []).cast<String>();
    return DatabaseEngine(
      id: json['id'] as String,
      name: json['name'] as String,
      category: json['category'] as String,
      description: json['description'] as String? ?? '',
      image: json['image'] as String? ?? '',
      versions: (json['versions'] as List<dynamic>)
          .map((e) => EngineVersion.fromJson(e as Map<String, dynamic>))
          .toList(),
      port: EnginePort.fromJson(json['port'] as Map<String, dynamic>),
      extraPorts: (json['extraPorts'] as List<dynamic>? ?? [])
          .map((e) => EnginePort.fromJson(e as Map<String, dynamic>))
          .toList(),
      auth: json['auth'] as String? ?? 'none',
      fixedUsername: json['fixedUsername'] as String?,
      usernameAllowed: json['usernameAllowed'] as bool? ?? false,
      defaultUsername: json['defaultUsername'] as String?,
      databaseNameAllowed: json['databaseNameAllowed'] as bool? ?? false,
      databaseNameLabel: json['databaseNameLabel'] as String?,
      architectures: strings('architectures'),
      persistent: json['persistent'] as bool? ?? true,
      minMemoryMb: json['minMemoryMb'] as int? ?? 0,
      defaultMemoryMb: json['defaultMemoryMb'] as int? ?? 512,
      defaultCpus: (json['defaultCpus'] as num?)?.toDouble() ?? 1,
      defaultStorageGb: json['defaultStorageGb'] as int? ?? 0,
      highAvailability: json['highAvailability'] as String?,
      rotation: json['rotation'] as String? ?? 'none',
      temporaryUsers: json['temporaryUsers'] as bool? ?? false,
      license: json['license'] as String? ?? '',
      requiresLicenseAcceptance:
          json['requiresLicenseAcceptance'] as bool? ?? false,
      editions: strings('editions'),
      notes: strings('notes'),
    );
  }
}

/// Marketplace category ids → display names, in display order.
const databaseCategories = <String, String>{
  'relational': 'Relational',
  'nosql': 'NoSQL',
  'cache': 'Cache & key-value',
  'analytics': 'Analytics',
  'vector': 'Vector',
};

/// The wizard's submission (POST /api/databases and /preview).
class DatabaseRequest {
  final String engine;
  final String version;
  final String name;
  final String serverId;
  final String databaseName;
  final String username;
  final int port;
  final String access;
  final int storageGb;
  final int memoryMb;
  final double cpus;
  final bool highAvailability;
  final String profile;
  final String? edition;
  final bool acceptLicense;
  final String backupCron;
  final bool consistentBackups;
  final int retentionDays;
  final int retentionCount;
  final String changeRequest;
  final String rollbackPlan;

  const DatabaseRequest({
    required this.engine,
    required this.version,
    required this.name,
    required this.serverId,
    required this.databaseName,
    required this.username,
    required this.port,
    required this.access,
    required this.storageGb,
    required this.memoryMb,
    required this.cpus,
    required this.highAvailability,
    required this.profile,
    required this.edition,
    required this.acceptLicense,
    required this.backupCron,
    required this.consistentBackups,
    required this.retentionDays,
    required this.retentionCount,
    this.changeRequest = '',
    this.rollbackPlan = '',
  });

  Map<String, dynamic> toJson() => {
    'engine': engine,
    'version': version,
    'name': name,
    'serverId': serverId,
    'databaseName': databaseName,
    'username': username,
    'port': port,
    'access': access,
    'storageGb': storageGb,
    'memoryMb': memoryMb,
    'cpus': cpus,
    'highAvailability': highAvailability,
    'profile': profile,
    if (edition != null) 'edition': edition,
    'acceptLicense': acceptLicense,
    'backup': {
      'cron': backupCron,
      'consistent': consistentBackups,
      'retentionDays': retentionDays,
      'retentionCount': retentionCount,
    },
    if (changeRequest.isNotEmpty) 'changeRequest': changeRequest,
    if (rollbackPlan.isNotEmpty) 'rollbackPlan': rollbackPlan,
  };
}

class DatabasePreview {
  final String composeYaml;
  final List<PublishedPort> extraPorts;
  final List<String> warnings;
  final String connectionString;
  final String username;
  final List<String> credentials;

  const DatabasePreview({
    required this.composeYaml,
    required this.extraPorts,
    required this.warnings,
    required this.connectionString,
    required this.username,
    required this.credentials,
  });

  factory DatabasePreview.fromJson(Map<String, dynamic> json) =>
      DatabasePreview(
        composeYaml: json['composeYaml'] as String,
        extraPorts: PublishedPort.listFrom(json['extraPorts']),
        warnings: (json['warnings'] as List<dynamic>? ?? []).cast<String>(),
        connectionString: json['connectionString'] as String? ?? '',
        username: json['username'] as String? ?? '',
        credentials: (json['credentials'] as List<dynamic>? ?? [])
            .cast<String>(),
      );
}

DateTime? _date(dynamic v) => v == null ? null : DateTime.parse(v as String);

/// A deployed database (GET /api/databases).
class DatabaseInstance {
  final String id;
  final String name;
  final String engine;
  final String engineName;
  final String category;
  final String version;
  final String deploymentId;
  final String serverId;
  final String serverName;
  final String databaseName;
  final String adminUsername;
  final int port;
  final String access;
  final String profile;
  final int storageGb;
  final int memoryMb;
  final double cpus;
  final bool highAvailability;
  final String? adminSecretId;
  final String? backupCron;
  final DateTime? backupNextRunAt;
  final DateTime? backupLastRunAt;
  final String? backupLastStatus;
  final bool backupConsistent;
  final int retentionDays;
  final int retentionCount;
  final String? createdBy;
  final DateTime createdAt;
  final String phase;
  final String healthStatus;
  final String connectionString;
  final String host;
  final String rotation;
  final bool temporaryUsers;
  final bool persistent;
  final List<PublishedPort> extraPorts;

  const DatabaseInstance({
    required this.id,
    required this.name,
    required this.engine,
    required this.engineName,
    required this.category,
    required this.version,
    required this.deploymentId,
    required this.serverId,
    required this.serverName,
    required this.databaseName,
    required this.adminUsername,
    required this.port,
    required this.access,
    required this.profile,
    required this.storageGb,
    required this.memoryMb,
    required this.cpus,
    required this.highAvailability,
    required this.adminSecretId,
    required this.backupCron,
    required this.backupNextRunAt,
    required this.backupLastRunAt,
    required this.backupLastStatus,
    required this.backupConsistent,
    required this.retentionDays,
    required this.retentionCount,
    required this.createdBy,
    required this.createdAt,
    required this.phase,
    required this.healthStatus,
    required this.connectionString,
    required this.host,
    required this.rotation,
    required this.temporaryUsers,
    required this.persistent,
    required this.extraPorts,
  });

  factory DatabaseInstance.fromJson(Map<String, dynamic> json) =>
      DatabaseInstance(
        id: json['id'] as String,
        name: json['name'] as String,
        engine: json['engine'] as String,
        engineName: json['engineName'] as String? ?? json['engine'] as String,
        category: json['category'] as String? ?? '',
        version: json['version'] as String,
        deploymentId: json['deploymentId'] as String,
        serverId: json['serverId'] as String,
        serverName: json['serverName'] as String? ?? '',
        databaseName: json['databaseName'] as String? ?? '',
        adminUsername: json['adminUsername'] as String? ?? '',
        port: json['port'] as int,
        access: json['access'] as String,
        profile: json['profile'] as String,
        storageGb: json['storageGb'] as int? ?? 0,
        memoryMb: json['memoryMb'] as int? ?? 0,
        cpus: (json['cpus'] as num?)?.toDouble() ?? 0,
        highAvailability: json['highAvailability'] as bool? ?? false,
        adminSecretId: json['adminSecretId'] as String?,
        backupCron: json['backupCron'] as String?,
        backupNextRunAt: _date(json['backupNextRunAt']),
        backupLastRunAt: _date(json['backupLastRunAt']),
        backupLastStatus: json['backupLastStatus'] as String?,
        backupConsistent: json['backupConsistent'] as bool? ?? true,
        retentionDays: json['retentionDays'] as int? ?? 0,
        retentionCount: json['retentionCount'] as int? ?? 0,
        createdBy: json['createdBy'] as String?,
        createdAt: DateTime.parse(json['createdAt'] as String),
        phase: json['phase'] as String? ?? 'unknown',
        healthStatus: json['healthStatus'] as String? ?? 'unknown',
        connectionString: json['connectionString'] as String? ?? '',
        host: json['host'] as String? ?? '',
        rotation: json['rotation'] as String? ?? 'none',
        temporaryUsers: json['temporaryUsers'] as bool? ?? false,
        persistent: json['persistent'] as bool? ?? true,
        extraPorts: PublishedPort.listFrom(json['extraPorts']),
      );
}

/// GET /api/databases/{id}: the instance plus its engine and backups.
class DatabaseDetail {
  final DatabaseInstance instance;
  final DatabaseEngine? engine;
  final List<Backup> backups;

  const DatabaseDetail({
    required this.instance,
    required this.engine,
    required this.backups,
  });

  factory DatabaseDetail.fromJson(Map<String, dynamic> json) => DatabaseDetail(
    instance: DatabaseInstance.fromJson(json),
    engine: json['engineInfo'] == null
        ? null
        : DatabaseEngine.fromJson(json['engineInfo'] as Map<String, dynamic>),
    backups: (json['backups'] as List<dynamic>? ?? [])
        .map((e) => Backup.fromJson(e as Map<String, dynamic>))
        .toList(),
  );
}

/// A vaulted credential's metadata. The value is never part of it — see
/// [ApiClient.revealSecret].
class DatabaseCredential {
  final String id;
  final String name;

  /// admin, token, or temporary.
  final String kind;
  final String username;
  final int version;
  final String? ownerId;
  final DateTime? expiresAt;
  final DateTime? downloadedAt;
  final DateTime? rotatedAt;
  final DateTime? revokedAt;
  final DateTime createdAt;
  final String masked;
  final bool active;
  final bool canReveal;
  final bool canManage;

  const DatabaseCredential({
    required this.id,
    required this.name,
    required this.kind,
    required this.username,
    required this.version,
    required this.ownerId,
    required this.expiresAt,
    required this.downloadedAt,
    required this.rotatedAt,
    required this.revokedAt,
    required this.createdAt,
    required this.masked,
    required this.active,
    required this.canReveal,
    required this.canManage,
  });

  bool get isTemporary => kind == 'temporary';

  factory DatabaseCredential.fromJson(Map<String, dynamic> json) =>
      DatabaseCredential(
        id: json['id'] as String,
        name: json['name'] as String,
        kind: json['kind'] as String,
        username: json['username'] as String? ?? '',
        version: json['version'] as int? ?? 1,
        ownerId: json['ownerId'] as String?,
        expiresAt: _date(json['expiresAt']),
        downloadedAt: _date(json['downloadedAt']),
        rotatedAt: _date(json['rotatedAt']),
        revokedAt: _date(json['revokedAt']),
        createdAt: DateTime.parse(json['createdAt'] as String),
        masked: json['masked'] as String? ?? '••••••••',
        active: json['active'] as bool? ?? false,
        canReveal: json['canReveal'] as bool? ?? false,
        canManage: json['canManage'] as bool? ?? false,
      );
}

class RevealedSecret {
  final String username;
  final String value;
  final int version;

  const RevealedSecret({
    required this.username,
    required this.value,
    required this.version,
  });

  factory RevealedSecret.fromJson(Map<String, dynamic> json) => RevealedSecret(
    username: json['username'] as String? ?? '',
    value: json['value'] as String,
    version: json['version'] as int? ?? 1,
  );
}

class SecretGrant {
  final String userId;
  final String email;
  final DateTime? expiresAt;
  final DateTime createdAt;

  const SecretGrant({
    required this.userId,
    required this.email,
    required this.expiresAt,
    required this.createdAt,
  });

  bool isExpired([DateTime? now]) =>
      expiresAt != null && !(now ?? DateTime.now()).isBefore(expiresAt!);

  factory SecretGrant.fromJson(Map<String, dynamic> json) => SecretGrant(
    userId: json['userId'] as String,
    email: json['email'] as String,
    expiresAt: _date(json['expiresAt']),
    createdAt: DateTime.parse(json['createdAt'] as String),
  );
}

class DirectoryUser {
  final String id;
  final String email;

  const DirectoryUser({required this.id, required this.email});

  factory DirectoryUser.fromJson(Map<String, dynamic> json) =>
      DirectoryUser(id: json['id'] as String, email: json['email'] as String);
}

/// POST /api/secrets/{id}/rotate.
class RotationResult {
  final DatabaseCredential credential;
  final String method;
  final String message;

  /// Set when rotation went through the deployment gate (redeploy
  /// engines): dispatched, pending_approval, scheduled, ...
  final String? deploymentStatus;

  const RotationResult({
    required this.credential,
    required this.method,
    required this.message,
    required this.deploymentStatus,
  });

  factory RotationResult.fromJson(Map<String, dynamic> json) => RotationResult(
    credential: DatabaseCredential.fromJson(
      json['credential'] as Map<String, dynamic>,
    ),
    method: json['method'] as String? ?? '',
    message: json['message'] as String? ?? '',
    deploymentStatus:
        (json['deployment'] as Map<String, dynamic>?)?['status'] as String?,
  );
}

/// POST /api/databases.
class DatabaseCreateResult {
  final DatabaseInstance database;
  final String? deploymentStatus;
  final List<String> warnings;

  const DatabaseCreateResult({
    required this.database,
    required this.deploymentStatus,
    required this.warnings,
  });

  factory DatabaseCreateResult.fromJson(Map<String, dynamic> json) =>
      DatabaseCreateResult(
        database: DatabaseInstance.fromJson(
          json['database'] as Map<String, dynamic>,
        ),
        deploymentStatus:
            (json['deployment'] as Map<String, dynamic>?)?['status'] as String?,
        warnings: (json['warnings'] as List<dynamic>? ?? []).cast<String>(),
      );
}
