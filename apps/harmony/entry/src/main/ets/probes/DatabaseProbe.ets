import { common } from '@kit.AbilityKit';
import { BusinessError } from '@kit.BasicServicesKit';
import { relationalStore } from '@kit.ArkData';
import { PROBE_LAUNCH, ProbeFailure, probeError } from './CapabilityProbe';

let databaseBusy: boolean = false;

@Component
export struct DatabaseProbe {
  @State private result: string = '尚未读取数据库';
  @State private busy: boolean = false;
  private store?: relationalStore.RdbStore;

  aboutToAppear(): void {
    this.run('读取');
  }

  private async release(): Promise<void> {
    const store = this.store;
    this.store = undefined;
    try {
      await store?.close();
    } catch (error) {
      this.result = probeError(error as BusinessError);
    }
  }

  private async database(): Promise<relationalStore.RdbStore> {
    if (this.store) {
      return this.store;
    }
    const context = this.getUIContext().getHostContext() as common.UIAbilityContext;
    const db = await relationalStore.getRdbStore(context, {
      name: 'h03_capability_probe.db', securityLevel: relationalStore.SecurityLevel.S1
    });
    this.store = db;
    await db.executeSql('CREATE TABLE IF NOT EXISTS h03_entity (id TEXT PRIMARY KEY, value TEXT NOT NULL)');
    await db.executeSql('CREATE TABLE IF NOT EXISTS h03_outbox (id TEXT PRIMARY KEY, entity_id TEXT NOT NULL)');
    await db.executeSql('CREATE TABLE IF NOT EXISTS h03_receipt (id INTEGER PRIMARY KEY, entity_id TEXT NOT NULL, launch TEXT NOT NULL)');
    return db;
  }

  private async count(db: relationalStore.RdbStore, table: string, id: string): Promise<number> {
    const rows = await db.querySql(`SELECT COUNT(*) AS n FROM ${table} WHERE id = ?`, [id]);
    try {
      return rows.goToFirstRow() ? rows.getLong(0) : -1;
    } finally {
      rows.close();
    }
  }

  private async read(db: relationalStore.RdbStore): Promise<string> {
    const rows = await db.querySql('SELECT entity_id, launch FROM h03_receipt WHERE id = 1');
    let id = '';
    let launch = '';
    try {
      if (rows.goToFirstRow()) {
        id = rows.getString(0);
        launch = rows.getString(1);
      }
    } finally {
      rows.close();
    }
    if (id.length === 0) {
      return '数据库已打开，尚无提交收据。';
    }
    const entity = await this.count(db, 'h03_entity', id);
    const outbox = await this.count(db, 'h03_outbox', id);
    if (entity !== 1 || outbox !== 1) {
      return `校验失败：实体 ${entity}，队列 ${outbox}`;
    }
    return `收据 ${id}\n实体与队列均为 1。${launch === PROBE_LAUNCH ?
      '同一进程读取；重启持久性尚未验证。' : '读取到上一进程提交；请与重启前收据核对。'}`;
  }

  private async commit(db: relationalStore.RdbStore): Promise<string> {
    const id = `commit-${Date.now()}`;
    db.beginTransaction();
    try {
      await db.executeSql('INSERT INTO h03_entity VALUES (?, ?)', [id, 'H03 synthetic']);
      await db.executeSql('INSERT INTO h03_outbox VALUES (?, ?)', [id, id]);
      await db.executeSql('INSERT OR REPLACE INTO h03_receipt VALUES (1, ?, ?)', [id, PROBE_LAUNCH]);
      db.commit();
    } catch (error) {
      db.rollBack();
      throw new ProbeFailure(error as BusinessError);
    }
    return await this.read(db);
  }

  private async rollback(db: relationalStore.RdbStore): Promise<string> {
    const before = await this.read(db);
    const id = `rollback-${Date.now()}`;
    let rejected = false;
    db.beginTransaction();
    try {
      await db.executeSql('INSERT INTO h03_entity VALUES (?, ?)', [id, 'H03 rollback']);
      await db.executeSql('INSERT INTO h03_outbox VALUES (?, ?)', [id, id]);
      try {
        await db.executeSql('INSERT INTO h03_outbox VALUES (?, ?)', [id, id]);
      } catch (error) {
        rejected = (error as BusinessError).code === 14800032;
        if (!rejected) {
          throw new ProbeFailure(error as BusinessError);
        }
      }
    } finally {
      db.rollBack();
    }
    const entity = await this.count(db, 'h03_entity', id);
    const outbox = await this.count(db, 'h03_outbox', id);
    const unchanged = before === await this.read(db);
    return rejected && entity === 0 && outbox === 0 && unchanged ?
      '唯一约束拒绝后回滚通过：实体 0、队列 0。已提交数据仍保留。' :
      `回滚校验失败：约束拒绝 ${rejected}，实体 ${entity}，队列 ${outbox}，原收据保持 ${unchanged}`;
  }

  private async run(action: string): Promise<void> {
    if (databaseBusy) {
      this.result = '上一页面的数据库操作尚未结束，请稍后重新读取。';
      return;
    }
    databaseBusy = true;
    this.busy = true;
    try {
      const db = await this.database();
      this.result = action === '提交' ? await this.commit(db) :
        action === '回滚' ? await this.rollback(db) : await this.read(db);
    } catch (error) {
      this.result = probeError(error as BusinessError);
    } finally {
      await this.release();
      this.busy = false;
      databaseBusy = false;
    }
  }

  build() {
    Column({ space: 12 }) {
      Text('关系数据库').fontSize(22).fontWeight(FontWeight.Bold)
      Text('仅写入 H03 合成数据。提交后关闭应用进程并重开，再核对收据；切换探针不算进程重启。')
      Button('事务提交').enabled(!this.busy).onClick(() => this.run('提交'))
      Button('约束失败与回滚').enabled(!this.busy).onClick(() => this.run('回滚'))
      Button('重新读取收据').enabled(!this.busy).onClick(() => this.run('读取'))
      Text(this.result).width('100%').fontSize(16).copyOption(CopyOptions.LocalDevice)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
