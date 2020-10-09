import 'dart:async';
import 'dart:convert';

import 'package:flutter/services.dart';

class BoltDB {
  static const MethodChannel _channel = const MethodChannel('boltdb');

  static Future<String> get platformVersion async {
    final String version = await _channel.invokeMethod('getPlatformVersion');
    return version;
  }

  static Future<String> execute(String params) async {
    return await _channel.invokeMethod('boltDB', params);
  }

  static Future<String> get close async {
    return await _channel.invokeMethod('close');
  }
}

class DB {
  final String file;

  DB(this.file);

  Future<void> createBucket(String bucket) async {
    await BoltDB.execute(P("bucket.create", _bucketParams(bucket)).toJson());
  }

  Future<List<String>> listBucket() async {
    var r = R.fromJson(await BoltDB.execute(P("bucket.list", _closeParams()).toJson()));
    if (r.error != null) {
      return Future.error(r.error.message);
    }
    return List<String>.from(r.result);
  }

  Future<void> deleteBucket(String bucket) async {
    await BoltDB.execute(P("bucket.delete", _bucketParams(bucket)).toJson());
  }

  Future<void> put(String bucket, String key, String value) async {
    await BoltDB.execute(P("key.put", _putParams(bucket, key, value)).toJson());
  }

  Future<void> get(String bucket, String key) async {
    await BoltDB.execute(P("key.get", _getParams(bucket, key)).toJson());
  }

  Future<void> delete(String bucket, String key) async {
    await BoltDB.execute(P("key.delete", _deleteParams(bucket, key)).toJson());
  }

  Future<List<Doc>> scan(String bucket, String prefix) async {
    var r = R.fromJson(await BoltDB.execute(P("key.scan", _scanParams(bucket, prefix)).toJson()));
    if (r.error != null) {
      return Future.error(r.error.message);
    }
    return List<Doc>.from(r.result.map((x) => Doc.fromMap(x)));
  }

  Future<void> close() async {
    await BoltDB.execute(P("db.close", _closeParams()).toJson());
  }

  Map<String, dynamic> _bucketParams(String bucket) => {"bucket": bucket, "file": file};
  Map<String, dynamic> _putParams(String bucket, String key, String value) => {"bucket": bucket, "key": key, "value": value, "file": file};
  Map<String, dynamic> _getParams(String bucket, String key) => {"bucket": bucket, "key": key, "file": file};
  Map<String, dynamic> _deleteParams(String bucket, String key) => {"bucket": bucket, "key": key, "file": file};
  Map<String, dynamic> _scanParams(String bucket, String prefix) => {"bucket": bucket, "prefix": prefix, "file": file};
  Map<String, dynamic> _closeParams() => {"file": file};
}

class P {
  P(this.method, this.params);

  String method;
  Map<String, dynamic> params;

  String toJson() => json.encode(toMap());

  Map<String, dynamic> toMap() => {"method": method, "params": params};
}

class Error {
  Error({this.code, this.message});

  final int code;
  final String message;

  factory Error.fromMap(Map<String, dynamic> json) => Error(
        code: json["code"] == null ? null : json["code"],
        message: json["message"] == null ? null : json["message"],
      );
}

class R {
  R({this.result, this.error});

  dynamic result;
  Error error;

  factory R.fromJson(String str) => R.fromMap(json.decode(str));

  factory R.fromMap(Map<String, dynamic> json) => R(
        error: json["error"] == null ? null : Error.fromMap(json["error"]),
        result: json["result"] == null ? null : json["result"],
      );
}

class Doc {
  Doc({this.key, this.value, this.size});

  String key;
  String value;
  int size;

  factory Doc.fromJson(String str) => Doc.fromMap(json.decode(str));
  factory Doc.fromMap(Map<String, dynamic> json) => Doc(key: json["k"], value: json["v"], size: json["s"]);
}
