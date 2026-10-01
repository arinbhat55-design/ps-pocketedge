import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:app/features/auth/login_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

class _StatusClient extends http.BaseClient {
  final int status;
  final String body;

  _StatusClient(this.status, [this.body = '{}']);

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async =>
      http.StreamedResponse(Stream.value(utf8.encode(body)), status);
}

void main() {
  test('a rejected token reports unauthorized', () async {
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _StatusClient(401, 'unauthorized'),
      authToken: 'expired',
    );
    var calls = 0;
    client.onUnauthorized = () => calls++;
    await expectLater(client.getMe(), throwsA(isA<ApiException>()));
    expect(calls, 1);
  });

  test('a failed login does not report unauthorized', () async {
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _StatusClient(401, 'invalid credentials'),
    );
    var calls = 0;
    client.onUnauthorized = () => calls++;
    await expectLater(
      client.login('a@example.com', 'wrong'),
      throwsA(isA<ApiException>()),
    );
    expect(calls, 0);
  });

  testWidgets('login screen can start a local session', (tester) async {
    String? token;
    await tester.pumpWidget(
      MaterialApp(
        home: LoginScreen(
          apiClient: ApiClient(
            baseUrl: 'http://localhost:8080',
            httpClient: _StatusClient(200, jsonEncode({'token': 'local'})),
          ),
          onLoggedIn: (_) {},
          onLocalSession: (t) => token = t,
          notice: 'Your session ended. Sign in to continue.',
        ),
      ),
    );
    expect(find.text('Your session ended. Sign in to continue.'), findsOne);
    await tester.tap(find.text('Use this computer without signing in'));
    await tester.pumpAndSettle();
    expect(token, 'local');
  });

  testWidgets('login screen explains why local access was refused', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: LoginScreen(
          apiClient: ApiClient(
            baseUrl: 'http://localhost:8080',
            httpClient: _StatusClient(
              403,
              'local access unavailable: login is required on this computer\n',
            ),
          ),
          onLoggedIn: (_) {},
          onLocalSession: (_) => fail('should not start a session'),
        ),
      ),
    );
    await tester.tap(find.text('Use this computer without signing in'));
    await tester.pumpAndSettle();
    expect(
      find.text('local access unavailable: login is required on this computer'),
      findsOne,
    );
  });

  testWidgets('login screen hides local access for remote servers', (
    tester,
  ) async {
    await tester.pumpWidget(
      MaterialApp(
        home: LoginScreen(
          apiClient: ApiClient(
            baseUrl: 'https://cp.example',
            httpClient: _StatusClient(200),
          ),
          onLoggedIn: (_) {},
        ),
      ),
    );
    expect(find.text('Use this computer without signing in'), findsNothing);
  });
}
