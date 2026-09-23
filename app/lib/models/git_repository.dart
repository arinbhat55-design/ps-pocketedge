/// Git hosts the control plane understands (they differ in default token
/// usernames and webhook formats). Value -> label.
const kGitProviders = {
  'github': 'GitHub',
  'gitlab': 'GitLab',
  'azure_devops': 'Azure DevOps',
  'bitbucket': 'Bitbucket',
  'generic': 'Other Git server',
};

/// A Git repository Compose files can be imported from — GET
/// /api/git-repositories. Credentials are write-only: [hasToken] says
/// whether one is configured.
class GitRepository {
  final String id;
  final String name;
  final String provider;
  final String url;
  final String username;
  final bool hasToken;
  final String defaultBranch;
  final DateTime createdAt;

  const GitRepository({
    required this.id,
    required this.name,
    required this.provider,
    required this.url,
    this.username = '',
    this.hasToken = false,
    this.defaultBranch = 'main',
    required this.createdAt,
  });

  String get providerLabel => kGitProviders[provider] ?? provider;

  factory GitRepository.fromJson(Map<String, dynamic> json) {
    return GitRepository(
      id: json['id'] as String,
      name: json['name'] as String,
      provider: json['provider'] as String? ?? 'generic',
      url: json['url'] as String,
      username: json['username'] as String? ?? '',
      hasToken: json['hasToken'] as bool? ?? false,
      defaultBranch: json['defaultBranch'] as String? ?? 'main',
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}

/// Where a repository's push webhook should point, and its secret.
class GitWebhookInfo {
  final String url;
  final String secret;

  const GitWebhookInfo({required this.url, required this.secret});

  factory GitWebhookInfo.fromJson(Map<String, dynamic> json) {
    return GitWebhookInfo(
      url: json['url'] as String,
      secret: json['secret'] as String,
    );
  }
}

class GitRef {
  final String name;
  final String commit;

  const GitRef({required this.name, required this.commit});

  factory GitRef.fromJson(Map<String, dynamic> json) {
    return GitRef(
      name: json['name'] as String,
      commit: json['commit'] as String,
    );
  }
}

/// A repository's branches and tags — what "Deploy from a selected
/// repository branch or tag" picks from.
class GitRefs {
  final List<GitRef> branches;
  final List<GitRef> tags;

  const GitRefs({this.branches = const [], this.tags = const []});

  factory GitRefs.fromJson(Map<String, dynamic> json) {
    List<GitRef> parse(Object? v) => (v as List<dynamic>? ?? [])
        .map((e) => GitRef.fromJson(e as Map<String, dynamic>))
        .toList();
    return GitRefs(
      branches: parse(json['branches']),
      tags: parse(json['tags']),
    );
  }
}

/// One commit that changed a Git-linked Compose file — the choices for
/// "Roll back to a previous commit".
class GitCommit {
  final String hash;
  final String message;
  final String author;
  final DateTime date;

  const GitCommit({
    required this.hash,
    required this.message,
    required this.author,
    required this.date,
  });

  factory GitCommit.fromJson(Map<String, dynamic> json) {
    return GitCommit(
      hash: json['hash'] as String,
      message: json['message'] as String? ?? '',
      author: json['author'] as String? ?? '',
      date: DateTime.parse(json['date'] as String),
    );
  }
}
