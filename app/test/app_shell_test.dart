import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

import 'package:app/api/api_client.dart';
import 'package:app/features/shell/app_shell.dart';

void main() {
  Future<void> pumpShell(WidgetTester tester, Size size) async {
    tester.view.physicalSize = size;
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final client = ApiClient(
      baseUrl: 'http://localhost:8080',
      httpClient: _FakeClient(),
    );
    await tester.pumpWidget(
      MaterialApp(
        home: AppShell(apiClient: client, onLogout: () {}),
      ),
    );
    await tester.pumpAndSettle();
  }

  testWidgets('phones get a bottom bar with a More sheet', (tester) async {
    await pumpShell(tester, const Size(390, 844));

    expect(find.byType(NavigationRail), findsNothing);
    expect(find.byType(NavigationBar), findsOneWidget);

    await tester.tap(find.text('More'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Volumes'));
    await tester.pumpAndSettle();

    // The More slot now shows which overflow module is open.
    expect(
      find.descendant(
        of: find.byType(NavigationBar),
        matching: find.text('Volumes'),
      ),
      findsOneWidget,
    );
  });

  testWidgets('desktop keeps the side rail', (tester) async {
    await pumpShell(tester, const Size(1280, 900));

    expect(find.byType(NavigationRail), findsOneWidget);
    expect(find.byType(NavigationBar), findsNothing);
  });
}

class _FakeClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = request.url.path == '/api/auth/me'
        ? jsonEncode({
            'id': 'u1',
            'email': 'me@example.com',
            'role': 'viewer',
            'createdAt': '2026-01-01T00:00:00Z',
          })
        : '[]';
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}
