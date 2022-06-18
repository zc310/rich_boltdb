import 'dart:convert';
import 'dart:io';

import 'package:convert/convert.dart';

import 'package:file_picker/file_picker.dart';
import 'package:filesize/filesize.dart';
import 'package:flutter/material.dart';

import 'package:boltdb/boltdb.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_sticky_header/flutter_sticky_header.dart';
import 'package:path_provider/path_provider.dart';
import 'package:path/path.dart' as p;
import 'dart:math' show Random;

import 'package:url_launcher/url_launcher_string.dart';

class MyApp extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'BoltDB Viewer',
      theme: ThemeData(primarySwatch: Colors.green),
      debugShowCheckedModeBanner: false,
      home: Home(),
    );
  }
}

class Home extends StatefulWidget {
  @override
  _HomeState createState() => _HomeState();
}

class _HomeState extends State<Home> {
  final _qController = TextEditingController();
  String _file = '';
  var _buckets = <String>[];
  var _expansion = Map<String, bool>();
  var _value = Map<String, List<Doc>?>();
  DB? _db;
  bool _search = false;
  String _searchText = "";

  @override
  void initState() {
    super.initState();
  }

  @override
  void dispose() {
    BoltDB.close;
    _qController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    var children = [
      _search
          ? SliverAppBar(
              leading: IconButton(
                tooltip: 'Back',
                icon: Icon(Icons.arrow_back),
                onPressed: () {
                  setState(() {
                    _value.keys.forEach((bucket) {
                      _value[bucket] = null;
                      _expansion[bucket] = false;
                    });

                    _search = false;
                  });
                },
              ),
              title: Theme(
                data: new ThemeData(
                  primaryColor: Colors.white,
                  primaryIconTheme: theme.primaryIconTheme.copyWith(color: Colors.grey),
                  primaryTextTheme: theme.textTheme,
                ),
                child: TextField(
                  controller: _qController,
                  textInputAction: TextInputAction.search,
                  onSubmitted: (value) => _query(value),
                  autofocus: true,
                  decoration: new InputDecoration(hintText: 'Prefix Scan...', suffixIcon: IconButton(onPressed: () => _query(_qController.text), icon: Icon(Icons.search))),
                ),
              ),
            )
          : SliverAppBar(
              leading: IconButton(icon: Icon(Icons.help), onPressed: () => _showHelp(context)),
              title: Text('BoltDB Viewer'),
              actions: [
                IconButton(
                    icon: Icon(Icons.search_outlined),
                    onPressed: () {
                      setState(() {
                        _search = true;
                      });
                    })
              ],
            ),
      SliverToBoxAdapter(
          child: _file == ""
              ? Row(
                  children: [
                    IconButton(icon: Icon(Icons.folder_open), onPressed: _openFile),
                    Expanded(
                      child: TextButton(
                        child: Text("Create a sample database file"),
                        onPressed: () async {
                          Directory tempDir = await getTemporaryDirectory();
                          _file = p.join(tempDir.path, "sample.db");
                          _db = DB(_file);
                          await _db?.createBucket("MyBucket_A");
                          List.generate(9, (index) => index).toList().forEach((element) {
                            _db?.put("MyBucket_A", "key_A_${element.toString().padLeft(6, "0")}",
                                String.fromCharCodes(List.generate(Random().nextInt(99), (index) => Random().nextInt(99))));
                          });

                          await _db?.createBucket("MyBucket_B");
                          List.generate(9, (index) => index).toList().forEach((element) {
                            _db?.put("MyBucket_B", "key_B_${element.toString().padLeft(6, "0")}",
                                String.fromCharCodes(List.generate(Random().nextInt(99), (index) => Random().nextInt(99))));
                          });

                          await _db?.createBucket("MyBucket_C");
                          List.generate(9, (index) => index).toList().forEach((element) {
                            _db?.put("MyBucket_C", "key_C_${element.toString().padLeft(6, "0")}",
                                String.fromCharCodes(List.generate(Random().nextInt(99), (index) => Random().nextInt(99))));
                          });

                          openFile();
                        },
                      ),
                    ),
                  ],
                )
              : Row(
                  children: [
                    IconButton(icon: Icon(Icons.folder_open), onPressed: _openFile),
                    Expanded(child: Center(child: Text(_file))),
                    IconButton(icon: Icon(Icons.login_outlined), onPressed: _closeFile),
                  ],
                )),
      SliverToBoxAdapter(child: Divider(height: 1))
    ];
    _buckets.forEach(
      (bucket) {
        children.add(
          SliverStickyHeader(
            header: ExpansionTile(
              backgroundColor: theme.cardColor,
              key: UniqueKey(),
              leading: Icon(Icons.storage),
              title: Text(bucket),
              initiallyExpanded: _expansion[bucket] ?? false,
              onExpansionChanged: (value) {
                if (_value[bucket] == null) {
                  _db?.scan(bucket, "").then((value) {
                    _value[bucket] = value;
                  }).catchError((error){
                    toast(error);
                  });
                }
                setState(() {
                  _expansion[bucket] = value;
                });
              },
            ),
            sliver: !(_expansion[bucket] ?? false) ? null : _buildItem(bucket),
          ),
        );
      },
    );

    return Scaffold(body: CustomScrollView(slivers: children));
  }

  void toast(String value, {int seconds = 3}) => ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          backgroundColor: Theme.of(context).primaryColor,
          content: Text(value),
          duration: Duration(seconds: seconds),
        ),
      );
  Widget _buildItem(String bucket) {
    final b = _value[bucket];
    if (b == null) {
      return SliverList(delegate: SliverChildBuilderDelegate((context, i) => null, childCount: 0));
    }

    return SliverList(
      delegate: SliverChildBuilderDelegate(
        (context, i) {
          final Doc d = b.elementAt(i);
          return Dismissible(
            background: Container(color: Colors.red),
            key: UniqueKey(),
            onDismissed: (direction) {
              _db?.delete(bucket, d.key);
              b.remove(d);
            },
            child: ListTile(
              leading: Icon(Icons.article_outlined),
              title: Text(d.key),
              trailing: Text(filesize(d.size), style: TextStyle(color: Colors.grey)),
              onTap: () => _showAlert(context, d),
            ),
          );
        },
        childCount: b.length,
      ),
    );
  }

  void openFile() {
    _clear();

    _db = DB(_file);
    _db?.listBucket().then((value) {
      value.forEach((element) {
        _expansion[element] = false;
      });

      setState(() {
        _buckets = value;
      });
    }).catchError((error) {
      toast(error);
    });
  }

  void _openFile() {
    FilePicker.platform.pickFiles().then((value) {
      if (value == null) {
        return null;
      }
      setState(() {
        _file = value.files.first.name;
      });
      openFile();
      print(_file);
    });
  }

  void _closeFile() {
    if (_db == null) {
      return;
    }
    _db?.close();

    setState(() {
      _file = "";
      _clear();
    });
  }

  void _clear() {
    _buckets.clear();
    _expansion.clear();
    _value.clear();
  }

  void _query(String value) {
    print(value);
    setState(() {
      if (value.isEmpty) {
        _searchText = "";
      } else {
        _searchText = value;
      }
      _value.keys.forEach((bucket) {
        _db?.scan(bucket, _searchText).then((value) {
          _value[bucket] = value;
          setState(() {
            _expansion[bucket] = value.length > 0;
          });
        }).catchError((error) {
          toast(error);
        });
      });
    });
  }

  Future _showAlert(BuildContext context, Doc doc) async {
    return showDialog(
        context: context,
        builder: (BuildContext context) {
          return new TextView(doc: doc);
        });
  }

  Future<void> _showHelp(BuildContext context) async {
    return showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (BuildContext context) {
        return AlertDialog(
          actions: [
            new ElevatedButton(
              child: Text("Close"),
              onPressed: () {
                Navigator.pop(context);
              },
            ),
          ],
          content: MarkdownBody(
              shrinkWrap: true,
              onTapLink: (String text, String? href, String title) async {
                if (href != null) {
                  await launchUrlString(href, mode: LaunchMode.externalApplication);
                }
              },
              selectable: true,
              data: '''[Bolt] is a pure Go key/value store inspired by [Howard Chu's][hyc_symas]
[LMDB project][lmdb]. The goal of the project is to provide a simple,
fast, and reliable database for projects that don't require a full database
server such as Postgres or MySQL.

Since Bolt is meant to be used as such a low-level piece of functionality,
simplicity is key. The API will be small and only focus on getting values
and setting values. That's it.

[bolt]: https://github.com/boltdb/bolt
[hyc_symas]: https://twitter.com/hyc_symas
[lmdb]: http://symas.com/mdb/
                        '''),
        );
      }, // user must tap button!
    );
  }
}

class TextView extends StatefulWidget {
  final Doc doc;

  const TextView({Key? key, required this.doc}) : super(key: key);
  @override
  _TextViewState createState() => _TextViewState();
}

class _TextViewState extends State<TextView> {
  int raw = 0;

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Column(
        children: [
          Text(widget.doc.key),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              ActionChip(
                  label: Text("Base64"),
                  backgroundColor: raw == 0 ? Colors.grey[400] : null,
                  onPressed: () {
                    setState(() {
                      raw = 0;
                    });
                  }),
              ActionChip(
                  backgroundColor: raw == 1 ? Colors.grey[400] : null,
                  label: Text("RAW"),
                  onPressed: () {
                    setState(() {
                      raw = 1;
                    });
                  }),
              ActionChip(
                  backgroundColor: raw == 2 ? Colors.grey[400] : null,
                  label: Text("Hex"),
                  onPressed: () {
                    setState(() {
                      raw = 2;
                    });
                  }),
            ],
          ),
        ],
      ),
      content: new TextField(
        keyboardType: TextInputType.multiline,
        maxLines: null,
        controller: TextEditingController(text: value()),
        readOnly: true,
      ),
      actions: <Widget>[new TextButton(onPressed: () => Navigator.pop(context), child: new Text('Ok'))],
    );
  }

  String value() {
    switch (raw) {
      case 1:
        try {
          return utf8.decode(base64.decode(widget.doc.value));
        } on Exception catch (e) {
          return '$e';
        }

      case 2:
        return hex.encode(base64.decode(widget.doc.value));
    }

    return widget.doc.value;
  }
}
