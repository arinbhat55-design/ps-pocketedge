import 'dart:convert';

import 'package:app/api/api_client.dart';
import 'package:app/features/settings/settings_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;

class _SettingsClient extends http.BaseClient {
  Map<String, dynamic>? update;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.method == 'PUT') {
      update = jsonDecode((request as http.Request).body) as Map<String, dynamic>;
    }
    final body = jsonEncode({
      'requireLocalLogin': update?['requireLocalLogin'] ?? false,
      'localListener': true,
    });
    return http.StreamedResponse(Stream.value(utf8.encode(body)), 200);
  }
}

void main() {
  testWidgets('requiring local login confirms the admin password', (
    tester,
  ) async {
    final transport = _SettingsClient();
    bool? changedTo;
    await tester.pumpWidget(
      MaterialApp(
        home: SettingsScreen(
          apiClient: ApiClient(
            baseUrl: 'http://localhost:8080',
            httpClient: transport,
          ),
          onAccessModeChanged: (value) => changedTo = value,
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();
    expect(transport.update, isNull);
    await tester.enterText(find.byType(TextField), 'known-password');
    await tester.tap(find.widgetWithText(FilledButton, 'Require login'));
    await tester.pumpAndSettle();
    expect(transport.update, {
      'requireLocalLogin': true,
      'password': 'known-password',
    });
    expect(changedTo, isTrue);
  });
}
