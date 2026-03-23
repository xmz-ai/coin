"use client";

import { Button, Card, Input, Space, Table, Tabs, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useMemo, useState } from "react";
import { AdminShell } from "../../components/AdminShell";
import { APIError, apiRequest } from "../../lib/api";

type OutboxItem = {
  id: number;
  event_id: string;
  txn_no: string;
  merchant_no: string;
  out_trade_no: string;
  status: string;
  retry_count: number;
  next_retry_at: string;
  updated_at: string;
  created_at: string;
};

type OutboxResponse = {
  items: OutboxItem[];
  next_page_token: string;
};

type AuditItem = {
  audit_id: number;
  request_id: string;
  operator_username: string;
  action: string;
  target_type: string;
  target_id: string;
  merchant_no: string;
  request_payload: string;
  result_code: string;
  result_message: string;
  created_at: string;
};

type AuditResponse = {
  items: AuditItem[];
  next_page_token: string;
};

function formatAPIError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return "request failed";
}

export default function NotifyPage(): JSX.Element {
  const [activeTab, setActiveTab] = useState("outbox");
  const [outboxMerchantNo, setOutboxMerchantNo] = useState("");
  const [outboxStatus, setOutboxStatus] = useState("");
  const [outboxTxnNo, setOutboxTxnNo] = useState("");
  const [outboxPageToken, setOutboxPageToken] = useState("");
  const [outboxPageSizeInput, setOutboxPageSizeInput] = useState("20");
  const [outboxRows, setOutboxRows] = useState<OutboxItem[]>([]);
  const [outboxNextToken, setOutboxNextToken] = useState("");
  const [outboxLoading, setOutboxLoading] = useState(false);

  const [auditMerchantNo, setAuditMerchantNo] = useState("");
  const [auditAction, setAuditAction] = useState("");
  const [auditOperator, setAuditOperator] = useState("");
  const [auditPageToken, setAuditPageToken] = useState("");
  const [auditPageSizeInput, setAuditPageSizeInput] = useState("20");
  const [auditRows, setAuditRows] = useState<AuditItem[]>([]);
  const [auditNextToken, setAuditNextToken] = useState("");
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditLoaded, setAuditLoaded] = useState(false);

  const [error, setError] = useState("");

  const outboxPageSize = useMemo(() => {
    const v = Number(outboxPageSizeInput);
    if (!Number.isFinite(v) || v <= 0) return 20;
    return Math.min(200, Math.trunc(v));
  }, [outboxPageSizeInput]);

  const auditPageSize = useMemo(() => {
    const v = Number(auditPageSizeInput);
    if (!Number.isFinite(v) || v <= 0) return 20;
    return Math.min(200, Math.trunc(v));
  }, [auditPageSizeInput]);

  const loadOutbox = async (cursor?: string): Promise<void> => {
    setOutboxLoading(true);
    setError("");
    try {
      const params = new URLSearchParams();
      params.set("page_size", String(outboxPageSize));
      if (outboxMerchantNo.trim()) params.set("merchant_no", outboxMerchantNo.trim());
      if (outboxStatus.trim()) params.set("status", outboxStatus.trim());
      if (outboxTxnNo.trim()) params.set("txn_no", outboxTxnNo.trim());
      const token = (cursor ?? outboxPageToken).trim();
      if (token) params.set("page_token", token);
      const data = await apiRequest<OutboxResponse>(`/notify/outbox-events?${params.toString()}`);
      setOutboxRows(data.items ?? []);
      setOutboxNextToken(data.next_page_token ?? "");
    } catch (err) {
      setError(formatAPIError(err));
    } finally {
      setOutboxLoading(false);
    }
  };

  const loadAudit = async (cursor?: string): Promise<void> => {
    setAuditLoading(true);
    setError("");
    try {
      const params = new URLSearchParams();
      params.set("page_size", String(auditPageSize));
      if (auditMerchantNo.trim()) params.set("merchant_no", auditMerchantNo.trim());
      if (auditAction.trim()) params.set("action", auditAction.trim());
      if (auditOperator.trim()) params.set("operator_username", auditOperator.trim());
      const token = (cursor ?? auditPageToken).trim();
      if (token) params.set("page_token", token);
      const data = await apiRequest<AuditResponse>(`/audit/logs?${params.toString()}`);
      setAuditRows(data.items ?? []);
      setAuditNextToken(data.next_page_token ?? "");
    } catch (err) {
      setError(formatAPIError(err));
    } finally {
      setAuditLoading(false);
    }
  };

  useEffect(() => {
    void loadOutbox("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (activeTab !== "audit" || auditLoaded) {
      return;
    }
    setAuditLoaded(true);
    void loadAudit("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, auditLoaded]);

  const outboxColumns: ColumnsType<OutboxItem> = [
    { title: "ID", dataIndex: "id", key: "id", width: 90 },
    { title: "Event", dataIndex: "event_id", key: "event_id", width: 280 },
    { title: "Txn", dataIndex: "txn_no", key: "txn_no", width: 250 },
    { title: "Merchant", dataIndex: "merchant_no", key: "merchant_no", width: 150 },
    {
      title: "Status",
      dataIndex: "status",
      key: "status",
      width: 120,
      render: (v) => {
        if (v === "SUCCESS") return <Tag color="green">{v}</Tag>;
        if (v === "DEAD") return <Tag color="red">{v}</Tag>;
        return <Tag color="blue">{v}</Tag>;
      },
    },
    { title: "Retry", dataIndex: "retry_count", key: "retry_count", width: 80 },
    { title: "Updated", dataIndex: "updated_at", key: "updated_at", width: 220 },
  ];

  const auditColumns: ColumnsType<AuditItem> = [
    { title: "Audit ID", dataIndex: "audit_id", key: "audit_id", width: 100 },
    { title: "Operator", dataIndex: "operator_username", key: "operator_username", width: 160 },
    { title: "Action", dataIndex: "action", key: "action", width: 220 },
    { title: "Target", dataIndex: "target_id", key: "target_id", width: 220 },
    { title: "Merchant", dataIndex: "merchant_no", key: "merchant_no", width: 150 },
    {
      title: "Result",
      key: "result",
      width: 200,
      render: (_, row) => `${row.result_code}: ${row.result_message}`,
    },
    { title: "Created", dataIndex: "created_at", key: "created_at", width: 220 },
  ];

  return (
    <AdminShell>
      <Typography.Title className="page-title">通知与审计</Typography.Title>
      {error ? (
        <Typography.Text type="danger" style={{ marginBottom: 8, display: "block" }}>
          {error}
        </Typography.Text>
      ) : null}

      <Tabs
        activeKey={activeTab}
        onChange={setActiveTab}
        items={[
          {
            key: "outbox",
            label: "Outbox 事件",
            children: (
              <Card>
                <Space wrap style={{ marginBottom: 12 }}>
                  <Input style={{ width: 180 }} value={outboxMerchantNo} onChange={(e) => setOutboxMerchantNo(e.target.value)} placeholder="merchant_no" />
                  <Input style={{ width: 180 }} value={outboxStatus} onChange={(e) => setOutboxStatus(e.target.value)} placeholder="status" />
                  <Input style={{ width: 220 }} value={outboxTxnNo} onChange={(e) => setOutboxTxnNo(e.target.value)} placeholder="txn_no" />
                  <Input style={{ width: 170 }} value={outboxPageToken} onChange={(e) => setOutboxPageToken(e.target.value)} placeholder="page_token" />
                  <Input style={{ width: 120 }} value={outboxPageSizeInput} onChange={(e) => setOutboxPageSizeInput(e.target.value)} placeholder="page_size" />
                  <Button type="primary" loading={outboxLoading} onClick={() => void loadOutbox()}>
                    查询
                  </Button>
                  <Button
                    disabled={!outboxNextToken}
                    onClick={() => {
                      setOutboxPageToken(outboxNextToken);
                      void loadOutbox(outboxNextToken);
                    }}
                  >
                    下一页
                  </Button>
                  {outboxNextToken ? <Tag color="blue">next_token: {outboxNextToken}</Tag> : <Tag>末页</Tag>}
                </Space>

                <Table<OutboxItem>
                  rowKey="id"
                  columns={outboxColumns}
                  dataSource={outboxRows}
                  loading={outboxLoading}
                  pagination={false}
                  scroll={{ x: 1450 }}
                />
              </Card>
            ),
          },
          {
            key: "audit",
            label: "审计日志",
            children: (
              <Card>
                <Space wrap style={{ marginBottom: 12 }}>
                  <Input style={{ width: 180 }} value={auditMerchantNo} onChange={(e) => setAuditMerchantNo(e.target.value)} placeholder="merchant_no" />
                  <Input style={{ width: 200 }} value={auditAction} onChange={(e) => setAuditAction(e.target.value)} placeholder="action" />
                  <Input style={{ width: 180 }} value={auditOperator} onChange={(e) => setAuditOperator(e.target.value)} placeholder="operator_username" />
                  <Input style={{ width: 170 }} value={auditPageToken} onChange={(e) => setAuditPageToken(e.target.value)} placeholder="page_token" />
                  <Input style={{ width: 120 }} value={auditPageSizeInput} onChange={(e) => setAuditPageSizeInput(e.target.value)} placeholder="page_size" />
                  <Button type="primary" loading={auditLoading} onClick={() => void loadAudit()}>
                    查询
                  </Button>
                  <Button
                    disabled={!auditNextToken}
                    onClick={() => {
                      setAuditPageToken(auditNextToken);
                      void loadAudit(auditNextToken);
                    }}
                  >
                    下一页
                  </Button>
                  {auditNextToken ? <Tag color="blue">next_token: {auditNextToken}</Tag> : <Tag>末页</Tag>}
                </Space>

                <Table<AuditItem>
                  rowKey="audit_id"
                  columns={auditColumns}
                  dataSource={auditRows}
                  loading={auditLoading}
                  pagination={false}
                  scroll={{ x: 1450 }}
                />
              </Card>
            ),
          },
        ]}
      />
    </AdminShell>
  );
}
