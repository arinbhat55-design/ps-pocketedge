class AppUser {
  final String id;
  final String email;
  final String role;
  final DateTime createdAt;

  const AppUser({
    required this.id,
    required this.email,
    required this.role,
    required this.createdAt,
  });

  bool get isAdmin => role == 'admin';

  factory AppUser.fromJson(Map<String, dynamic> json) {
    return AppUser(
      id: json['id'] as String,
      email: json['email'] as String,
      role: json['role'] as String,
      createdAt: DateTime.parse(json['createdAt'] as String),
    );
  }
}
