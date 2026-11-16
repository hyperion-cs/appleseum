CREATE TABLE accounts (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL DEFAULT '',
 profile BLOB,
 profile_error TEXT NOT NULL DEFAULT '',
 profile_attempted_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE account_logins (
 login TEXT PRIMARY KEY,
 account_id TEXT NOT NULL REFERENCES accounts(id)
);
CREATE INDEX account_logins_account ON account_logins(account_id,login);

CREATE TABLE devices (
 id TEXT PRIMARY KEY,
 account_id TEXT REFERENCES accounts(id),
 uuid BLOB NOT NULL UNIQUE CHECK(length(uuid)=16),
 local_user_id TEXT NOT NULL,
 model TEXT NOT NULL,
 os_version TEXT NOT NULL,
 language TEXT NOT NULL,
 locale TEXT NOT NULL,
 compliance_id TEXT NOT NULL,
 time_zone TEXT NOT NULL,
 idms_generation INTEGER NOT NULL DEFAULT 0,
 idms BLOB,
 routing TEXT NOT NULL DEFAULT '0',
 amd BLOB,
 storefront TEXT NOT NULL DEFAULT ''
);
CREATE INDEX devices_account ON devices(account_id,id);

CREATE TABLE sessions (
 id TEXT PRIMARY KEY,
 scope TEXT NOT NULL CHECK(scope IN ('local','public')),
 device_id TEXT REFERENCES devices(id),
 csrf TEXT NOT NULL,
 expires_at INTEGER,
 authenticated_at TEXT NOT NULL DEFAULT '',
 last_success_at TEXT NOT NULL DEFAULT '',
 storefront TEXT NOT NULL DEFAULT '',
 pod TEXT NOT NULL DEFAULT '',
 bag BLOB,
 secrets BLOB
);
CREATE UNIQUE INDEX sessions_local ON sessions(scope) WHERE scope='local';
CREATE INDEX sessions_device ON sessions(device_id);
CREATE INDEX sessions_expiry ON sessions(expires_at) WHERE scope='public';

CREATE TABLE preferences (
 session_id TEXT PRIMARY KEY,
 language TEXT NOT NULL DEFAULT 'en' CHECK(language IN ('ru','en')),
 theme TEXT NOT NULL DEFAULT 'dark' CHECK(theme IN ('light','dark')),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0)
);

CREATE TABLE table_preferences (
 session_id TEXT NOT NULL,
 resource TEXT NOT NULL CHECK(resource IN ('catalog','library','shares')),
 sort TEXT NOT NULL,
 direction TEXT NOT NULL CHECK(direction IN ('asc','desc')),
 delisted INTEGER NOT NULL DEFAULT 0 CHECK(delisted IN (0,1)),
 mine INTEGER NOT NULL DEFAULT 0 CHECK(mine IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 PRIMARY KEY(session_id,resource)
);

CREATE TABLE catalog (
 id TEXT NOT NULL UNIQUE,
 account_id TEXT NOT NULL,
 app_id INTEGER NOT NULL,
 name TEXT NOT NULL,
 delisted INTEGER NOT NULL,
 size INTEGER CHECK(size IS NULL OR size>0),
 purchased_at TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(account_id,app_id)
);
CREATE INDEX catalog_owner_page ON catalog(account_id,id);
CREATE INDEX catalog_name ON catalog(account_id,name COLLATE APP_TEXT,id);
CREATE INDEX catalog_size ON catalog(account_id,COALESCE(size,-1),id);
CREATE INDEX catalog_purchased ON catalog(account_id,purchased_at,id);
CREATE INDEX catalog_delisted_page ON catalog(account_id,delisted,id);
CREATE INDEX catalog_delisted_name ON catalog(account_id,delisted,name COLLATE APP_TEXT,id);
CREATE INDEX catalog_delisted_size ON catalog(account_id,delisted,COALESCE(size,-1),id);
CREATE INDEX catalog_delisted_purchased ON catalog(account_id,delisted,purchased_at,id);

CREATE TABLE app_assets (
 account_id TEXT NOT NULL,
 app_id INTEGER NOT NULL,
 icon TEXT NOT NULL,
 source_url TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(account_id,app_id)
);
CREATE INDEX app_assets_icon ON app_assets(icon,account_id,app_id);

CREATE TABLE app_version_lists (
 account_id TEXT NOT NULL REFERENCES accounts(id),
 app_id INTEGER NOT NULL,
 revision TEXT NOT NULL,
 fetched_at TEXT NOT NULL,
 latest TEXT NOT NULL,
 PRIMARY KEY(account_id,app_id)
);

CREATE TABLE app_versions (
 account_id TEXT NOT NULL,
 app_id INTEGER NOT NULL,
 version_id TEXT NOT NULL,
 position INTEGER NOT NULL,
 version TEXT NOT NULL DEFAULT '',
 build TEXT NOT NULL DEFAULT '',
 minimum_os TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(account_id,app_id,version_id),
 FOREIGN KEY(account_id,app_id) REFERENCES app_version_lists(account_id,app_id) ON DELETE CASCADE
);
CREATE INDEX app_versions_position ON app_versions(account_id,app_id,position,version_id) WHERE position>=0;

-- A package without a size is still transferring; its download row tracks progress.
CREATE TABLE packages (
 id TEXT PRIMARY KEY,
 account_id TEXT NOT NULL REFERENCES accounts(id),
 app_id INTEGER NOT NULL,
 name TEXT NOT NULL,
 created_at TEXT NOT NULL,
 bundle TEXT NOT NULL DEFAULT '',
 version TEXT NOT NULL DEFAULT '',
 display_version TEXT NOT NULL DEFAULT '',
 minimum_os TEXT NOT NULL DEFAULT '',
 size INTEGER CHECK(size>=0)
);
CREATE INDEX packages_account ON packages(account_id,id);
CREATE INDEX packages_account_app ON packages(account_id,app_id,id,size);
CREATE INDEX packages_app ON packages(app_id,id);
CREATE INDEX packages_name ON packages(name COLLATE APP_TEXT,id);
CREATE INDEX packages_created ON packages(created_at,id);

CREATE TABLE downloads (
 package_id TEXT PRIMARY KEY REFERENCES packages(id) ON DELETE CASCADE,
 session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
 version_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('queued','downloading','preparing','canceling','done','failed','canceled')),
 received INTEGER NOT NULL DEFAULT 0,
 total INTEGER NOT NULL DEFAULT 0,
 speed REAL NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX downloads_queue ON downloads(session_id,status,package_id);

CREATE TABLE shares (
 id TEXT PRIMARY KEY,
 created_at TEXT NOT NULL,
 creator_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
 owner_id TEXT NOT NULL DEFAULT '',
 owner_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX shares_owner_created ON shares(owner_id,created_at,id);
CREATE INDEX shares_created ON shares(created_at,id);

CREATE TABLE share_packages (
 share_id TEXT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
 package_id TEXT NOT NULL REFERENCES packages(id) ON DELETE CASCADE,
 PRIMARY KEY(share_id,package_id)
);
CREATE INDEX share_packages_package ON share_packages(package_id);

CREATE VIEW ready_packages AS
 SELECT id,account_id,app_id,name,created_at,bundle,version,display_version,minimum_os,size,
  COALESCE(NULLIF(display_version,''),version) AS shown_version
 FROM packages WHERE size IS NOT NULL;

CREATE VIEW transfers AS
 SELECT p.id,p.account_id,p.app_id,p.name,p.created_at,d.session_id,d.version_id,d.status,d.received,d.total,d.speed,d.error
 FROM downloads d JOIN packages p ON p.id=d.package_id;

-- Published files, then transfers that neither finished nor were canceled.
CREATE VIEW library AS
 SELECT id,account_id,app_id,name,shown_version AS version,minimum_os,size,created_at,
  'done' AS status,size AS received,size AS total,0 AS speed,'' AS error
 FROM ready_packages
 UNION ALL
 SELECT id,account_id,app_id,name,'','',total,created_at,status,received,total,speed,error
 FROM transfers WHERE status NOT IN ('done','canceled');

-- A share is named after its first app in display order.
CREATE VIEW share_list AS
 SELECT s.id,s.owner_id,s.owner_name,s.created_at,
  (SELECT count(*) FROM share_packages sp WHERE sp.share_id=s.id) AS count,
  p.name,COALESCE(a.icon,'') AS icon
 FROM shares s
 JOIN packages p ON p.id=(SELECT sp.package_id FROM share_packages sp JOIN packages candidate ON candidate.id=sp.package_id
  WHERE sp.share_id=s.id ORDER BY candidate.name COLLATE APP_TEXT,candidate.id LIMIT 1)
 LEFT JOIN app_assets a ON a.account_id=p.account_id AND a.app_id=p.app_id;

-- Names are folded once by fold(); the trigram index matches substrings of three or more characters.
CREATE TABLE search_documents (
 id INTEGER PRIMARY KEY,
 source TEXT NOT NULL CHECK(source IN ('catalog','packages','shares')),
 entity_id TEXT NOT NULL,
 name TEXT NOT NULL,
 UNIQUE(source,entity_id)
);
CREATE VIRTUAL TABLE search_names USING fts5(
 name, content='search_documents', content_rowid='id', tokenize='trigram case_sensitive 1'
);
CREATE TRIGGER search_documents_insert AFTER INSERT ON search_documents BEGIN
 INSERT INTO search_names(rowid,name) VALUES(new.id,new.name);
END;
CREATE TRIGGER search_documents_delete AFTER DELETE ON search_documents BEGIN
 INSERT INTO search_names(search_names,rowid,name) VALUES('delete',old.id,old.name);
END;
CREATE TRIGGER search_documents_update AFTER UPDATE OF name ON search_documents
 WHEN old.name IS NOT new.name BEGIN
 INSERT INTO search_names(search_names,rowid,name) VALUES('delete',old.id,old.name);
 INSERT INTO search_names(rowid,name) VALUES(new.id,new.name);
END;

CREATE TRIGGER search_catalog_insert AFTER INSERT ON catalog BEGIN
 INSERT INTO search_documents(source,entity_id,name) VALUES('catalog',new.id,fold(new.name));
END;
CREATE TRIGGER search_catalog_delete AFTER DELETE ON catalog BEGIN
 DELETE FROM search_documents WHERE source='catalog' AND entity_id=old.id;
END;
CREATE TRIGGER search_catalog_update AFTER UPDATE OF id,name ON catalog BEGIN
 UPDATE search_documents SET entity_id=new.id,name=fold(new.name)
  WHERE source='catalog' AND entity_id=old.id;
END;

CREATE TRIGGER search_packages_insert AFTER INSERT ON packages BEGIN
 INSERT INTO search_documents(source,entity_id,name) VALUES('packages',new.id,fold(new.name));
END;
CREATE TRIGGER search_packages_delete AFTER DELETE ON packages BEGIN
 DELETE FROM search_documents WHERE source='packages' AND entity_id=old.id;
END;
CREATE TRIGGER search_packages_update AFTER UPDATE OF id,name ON packages BEGIN
 UPDATE search_documents SET entity_id=new.id,name=fold(new.name)
  WHERE source='packages' AND entity_id=old.id;
END;

CREATE TRIGGER search_shares_insert AFTER INSERT ON shares BEGIN
 INSERT INTO search_documents(source,entity_id,name) VALUES('shares',new.id,fold(new.owner_name));
END;
CREATE TRIGGER search_shares_delete AFTER DELETE ON shares BEGIN
 DELETE FROM search_documents WHERE source='shares' AND entity_id=old.id;
END;
CREATE TRIGGER search_shares_update AFTER UPDATE OF id,owner_name ON shares BEGIN
 UPDATE search_documents SET entity_id=new.id,name=fold(new.owner_name)
  WHERE source='shares' AND entity_id=old.id;
END;

PRAGMA user_version=1;
