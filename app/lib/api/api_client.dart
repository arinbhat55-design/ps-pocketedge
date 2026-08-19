import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/server.dart';

/// Thin REST client for the control plane's JSON API.
///
/// M2 scope: read-only, no auth yet (JWT bearer auth lands alongside the
/// admin login flow in M3).
class ApiClient {
  final String baseUrl;
  final http.Client _http;

  ApiClient({required this.baseUrl, http.Client? httpClient})
      : _http = httpClient ?? http.Client();

  Future<List<Server>> listServers() async {
    final response = await _http.get(Uri.parse('$baseUrl/api/servers'));
    if (response.statusCode != 200) {
      throw Exception(
          'failed to list servers: ${response.statusCode} ${response.body}');
    }
    final decoded = jsonDecode(response.body) as List<dynamic>;
    return decoded
        .map((e) => Server.fromJson(e as Map<String, dynamic>))
        .toList();
  }
}
