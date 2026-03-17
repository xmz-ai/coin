# 核心交易正向时序图（Transfer）

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant API as API(BusinessHandler)
    participant RT as TransferRoutingService
    participant TS as TransferService
    participant AP as TransferAsyncProcessor
    participant RW as TransferRecoveryWorker
    participant DB as Postgres(Repository)
    participant OW as WebhookWorker
    participant WH as Merchant Webhook

    C->>API: POST /api/v1/transactions/credit|debit|transfer
    API->>API: 验签、参数校验、账户解析
    API->>RT: Resolve(scene + debit/credit)
    RT-->>API: debit_account_no + credit_account_no
    API->>TS: Submit(txn INIT)
    TS->>DB: CreateTransferTxn(status=INIT)
    DB-->>TS: txn_no
    TS-->>API: txn_no
    API->>AP: Enqueue(txn_no)
    API-->>C: 201 {txn_no, status=INIT}

    AP->>DB: GetTransferTxn(txn_no)
    AP->>DB: TransitionTransferTxnStatus(INIT -> PROCESSING)

    AP->>DB: ApplyTransferDebitStage(txn_no, debit_account_no, amount)
    Note over DB: 事务内(出账阶段)\n锁 txn + 锁 debit account\n能力/余额校验 + 扣减 + 写 account_change_log\nbook_enabled=true 时 FEFO 扣减 account_book 并写 account_book_change_log\nstatus -> PAY_SUCCESS

    AP->>DB: ApplyTransferCreditStage(txn_no, credit_account_no, amount)
    Note over DB: 事务内(入账阶段)\n锁 txn + 锁 credit account\n入账 + 写 account_change_log\nbook_enabled=true 时要求 credit_expire_at 并写 account_book/account_book_change_log\nstatus -> RECV_SUCCESS + Insert outbox_event

    alt 任一阶段失败
        AP->>DB: TransitionTransferTxnStatus(current -> FAILED, error_code, error_msg)
    end

    Note over RW: 兜底补偿轮询 INIT/PROCESSING/PAY_SUCCESS 并重试 Process(txn_no)
    RW->>AP: Process(txn_no)

    Note over OW: RECV_SUCCESS 后处理 outbox 并投递 webhook
    OW->>DB: ClaimDueOutboxEvents(batch)
    OW->>WH: POST webhook(签名)
    alt HTTP 2xx
        OW->>DB: MarkOutboxEventSuccess + InsertNotifyLog(SUCCESS)
    else 非2xx/异常
        OW->>DB: MarkOutboxEventRetry(next_retry_at/DEAD) + InsertNotifyLog(FAILED/DEAD)
    end
```
