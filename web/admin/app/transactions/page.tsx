"use client";

import {
  Button,
  Card,
  Drawer,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useMemo, useState } from "react";
import { AdminShell } from "../../components/AdminShell";
import { APIError, apiRequest } from "../../lib/api";

type TxnItem = {
  txn_no: string;
  out_trade_no: string;
  title: string;
  remark: string;
  transfer_scene: string;
  status: string;
  amount: number;
  refundable_amount: number;
  debit_account_no: string;
  credit_account_no: string;
  error_code: string;
  error_msg: string;
  created_at: string;
};

type TxnListResponse = {
  items: TxnItem[];
  next_page_token: string;
};

function formatAPIError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return "request failed";
}

export default function TransactionsPage(): JSX.Element {
  const [merchantNo, setMerchantNo] = useState("");
  const [outTradeNo, setOutTradeNo] = useState("");
  const [status, setStatus] = useState("");
  const [scene, setScene] = useState("");
  const [outUserID, setOutUserID] = useState("");
  const [pageToken, setPageToken] = useState("");
  const [pageSizeInput, setPageSizeInput] = useState("20");

  const [rows, setRows] = useState<TxnItem[]>([]);
  const [nextToken, setNextToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [createOpen, setCreateOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const [detailOpen, setDetailOpen] = useState(false);
  const [detailTxnNo, setDetailTxnNo] = useState("");
  const [detailJSON, setDetailJSON] = useState("");

  const pageSize = useMemo(() => {
    const v = Number(pageSizeInput);
    if (!Number.isFinite(v) || v <= 0) {
      return 20;
    }
    return Math.min(200, Math.trunc(v));
  }, [pageSizeInput]);

  const loadTransactions = async (cursor?: string): Promise<void> => {
    setLoading(true);
    setError("");
    try {
      if (outTradeNo.trim()) {
        if (!merchantNo.trim()) {
          setError("merchant_no is required when querying by out_trade_no");
          return;
        }
        const params = new URLSearchParams();
        params.set("merchant_no", merchantNo.trim());
        params.set("out_trade_no", outTradeNo.trim());
        const one = await apiRequest<TxnItem>(`/transactions/by-out-trade-no?${params.toString()}`);
        setRows([one]);
        setNextToken("");
        return;
      }

      const params = new URLSearchParams();
      params.set("merchant_no", merchantNo.trim());
      params.set("page_size", String(pageSize));
      if (status.trim()) params.set("status", status.trim());
      if (scene.trim()) params.set("transfer_scene", scene.trim());
      if (outUserID.trim()) params.set("out_user_id", outUserID.trim());
      const token = (cursor ?? pageToken).trim();
      if (token) params.set("page_token", token);

      const data = await apiRequest<TxnListResponse>(`/transactions?${params.toString()}`);
      setRows(data.items ?? []);
      setNextToken(data.next_page_token ?? "");
    } catch (err) {
      setError(formatAPIError(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadTransactions("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openDetail = async (txnNo: string): Promise<void> => {
    setDetailTxnNo(txnNo);
    setDetailJSON("");
    setDetailOpen(true);
    try {
      const data = await apiRequest<TxnItem>(`/transactions/${encodeURIComponent(txnNo)}`);
      setDetailJSON(JSON.stringify(data, null, 2));
    } catch (err) {
      setDetailJSON(formatAPIError(err));
    }
  };

  const submitTxn = async (path: string, payload: Record<string, unknown>): Promise<void> => {
    setSubmitting(true);
    try {
      const data = await apiRequest(path, {
        method: "POST",
        body: JSON.stringify(payload),
      });
      message.success("下单成功");
      Modal.info({ title: "提交结果", width: 760, content: <pre className="result-box">{JSON.stringify(data, null, 2)}</pre> });
      setCreateOpen(false);
      setPageToken("");
      setOutTradeNo("");
      await loadTransactions("");
    } catch (err) {
      message.error(formatAPIError(err));
    } finally {
      setSubmitting(false);
    }
  };

  const columns: ColumnsType<TxnItem> = [
    { title: "Created At", dataIndex: "created_at", key: "created_at", width: 220 },
    { title: "Txn No", dataIndex: "txn_no", key: "txn_no", width: 260 },
    { title: "Out Trade", dataIndex: "out_trade_no", key: "out_trade_no", width: 170 },
    { title: "Title", dataIndex: "title", key: "title", width: 180 },
    { title: "Remark", dataIndex: "remark", key: "remark", width: 220 },
    { title: "Scene", dataIndex: "transfer_scene", key: "transfer_scene", width: 110 },
    {
      title: "Status",
      dataIndex: "status",
      key: "status",
      width: 130,
      render: (v) => {
        if (v === "RECV_SUCCESS") return <Tag color="green">{v}</Tag>;
        if (v === "FAILED") return <Tag color="red">{v}</Tag>;
        return <Tag color="blue">{v}</Tag>;
      },
    },
    { title: "Amount", dataIndex: "amount", key: "amount", width: 110 },
    { title: "Debit", dataIndex: "debit_account_no", key: "debit_account_no", width: 210 },
    { title: "Credit", dataIndex: "credit_account_no", key: "credit_account_no", width: 210 },
    {
      title: "Action",
      key: "action",
      width: 120,
      render: (_, row) => <Button onClick={() => void openDetail(row.txn_no)}>详情</Button>,
    },
  ];

  return (
    <AdminShell>
      <Typography.Title className="page-title">交易管理</Typography.Title>

      <Card style={{ marginBottom: 12 }}>
        <Space wrap>
          <Input style={{ width: 200 }} value={merchantNo} onChange={(e) => setMerchantNo(e.target.value)} placeholder="merchant_no" />
          <Input style={{ width: 180 }} value={outTradeNo} onChange={(e) => setOutTradeNo(e.target.value)} placeholder="out_trade_no(精确查)" />
          <Select
            style={{ width: 150 }}
            value={status || undefined}
            onChange={(v) => setStatus(v ?? "")}
            allowClear
            placeholder="status"
            options={[
              { value: "INIT", label: "INIT" },
              { value: "PAY_SUCCESS", label: "PAY_SUCCESS" },
              { value: "RECV_SUCCESS", label: "RECV_SUCCESS" },
              { value: "FAILED", label: "FAILED" },
            ]}
          />
          <Select
            style={{ width: 140 }}
            value={scene || undefined}
            onChange={(v) => setScene(v ?? "")}
            allowClear
            placeholder="scene"
            options={[
              { value: "ISSUE", label: "ISSUE" },
              { value: "CONSUME", label: "CONSUME" },
              { value: "P2P", label: "P2P" },
            ]}
          />
          <Input style={{ width: 160 }} value={outUserID} onChange={(e) => setOutUserID(e.target.value)} placeholder="out_user_id" />
          <Input style={{ width: 170 }} value={pageToken} onChange={(e) => setPageToken(e.target.value)} placeholder="page_token" />
          <Input style={{ width: 120 }} value={pageSizeInput} onChange={(e) => setPageSizeInput(e.target.value)} placeholder="page_size" />
          <Button type="primary" loading={loading} onClick={() => void loadTransactions()}>
            查询列表
          </Button>
          <Button
            disabled={!nextToken}
            onClick={() => {
              setPageToken(nextToken);
              void loadTransactions(nextToken);
            }}
          >
            下一页
          </Button>
          <Button onClick={() => setCreateOpen(true)}>新建交易</Button>
          {nextToken ? <Tag color="blue">next_token: {nextToken}</Tag> : <Tag>末页</Tag>}
        </Space>
        {error ? (
          <Typography.Text type="danger" style={{ display: "block", marginTop: 8 }}>
            {error}
          </Typography.Text>
        ) : null}
      </Card>

      <Card>
        <Table<TxnItem>
          rowKey="txn_no"
          columns={columns}
          dataSource={rows}
          loading={loading}
          pagination={false}
          scroll={{ x: 2100 }}
        />
      </Card>

      <Modal title="新建交易" open={createOpen} onCancel={() => setCreateOpen(false)} footer={null} width={780} destroyOnClose>
        <Tabs
          items={[
            {
              key: "credit",
              label: "发币",
              children: (
                <Form
                  layout="vertical"
                  initialValues={{ merchant_no: merchantNo }}
                  onFinish={(values) =>
                    void submitTxn("/transactions/credit", {
                      merchant_no: values.merchant_no,
                      out_trade_no: values.out_trade_no,
                      title: values.title,
                      remark: values.remark,
                      user_id: values.user_id,
                      credit_account_no: values.credit_account_no,
                      debit_account_no: values.debit_account_no,
                      amount: Number(values.amount),
                      expire_in_days: values.expire_in_days ? Number(values.expire_in_days) : 0,
                    })
                  }
                >
                  <Form.Item name="merchant_no" label="merchant_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="out_trade_no" label="out_trade_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="title" label="title"><Input /></Form.Item>
                  <Form.Item name="remark" label="remark"><Input.TextArea rows={3} /></Form.Item>
                  <Form.Item name="user_id" label="user_id"><Input /></Form.Item>
                  <Form.Item name="credit_account_no" label="credit_account_no"><Input /></Form.Item>
                  <Form.Item name="debit_account_no" label="debit_account_no"><Input /></Form.Item>
                  <Form.Item name="amount" label="amount" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="expire_in_days" label="expire_in_days"><Input /></Form.Item>
                  <Button loading={submitting} type="primary" htmlType="submit">提交</Button>
                </Form>
              ),
            },
            {
              key: "debit",
              label: "扣币",
              children: (
                <Form
                  layout="vertical"
                  initialValues={{ merchant_no: merchantNo }}
                  onFinish={(values) =>
                    void submitTxn("/transactions/debit", {
                      merchant_no: values.merchant_no,
                      out_trade_no: values.out_trade_no,
                      title: values.title,
                      remark: values.remark,
                      debit_account_no: values.debit_account_no,
                      debit_out_user_id: values.debit_out_user_id,
                      credit_account_no: values.credit_account_no,
                      credit_out_user_id: values.credit_out_user_id,
                      amount: Number(values.amount),
                    })
                  }
                >
                  <Form.Item name="merchant_no" label="merchant_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="out_trade_no" label="out_trade_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="title" label="title"><Input /></Form.Item>
                  <Form.Item name="remark" label="remark"><Input.TextArea rows={3} /></Form.Item>
                  <Form.Item name="debit_account_no" label="debit_account_no"><Input /></Form.Item>
                  <Form.Item name="debit_out_user_id" label="debit_out_user_id"><Input /></Form.Item>
                  <Form.Item name="credit_account_no" label="credit_account_no"><Input /></Form.Item>
                  <Form.Item name="credit_out_user_id" label="credit_out_user_id"><Input /></Form.Item>
                  <Form.Item name="amount" label="amount" rules={[{ required: true }]}><Input /></Form.Item>
                  <Button loading={submitting} type="primary" htmlType="submit">提交</Button>
                </Form>
              ),
            },
            {
              key: "transfer",
              label: "转账",
              children: (
                <Form
                  layout="vertical"
                  initialValues={{ merchant_no: merchantNo }}
                  onFinish={(values) =>
                    void submitTxn("/transactions/transfer", {
                      merchant_no: values.merchant_no,
                      out_trade_no: values.out_trade_no,
                      title: values.title,
                      remark: values.remark,
                      from_account_no: values.from_account_no,
                      from_out_user_id: values.from_out_user_id,
                      to_account_no: values.to_account_no,
                      to_out_user_id: values.to_out_user_id,
                      to_expire_in_days: values.to_expire_in_days ? Number(values.to_expire_in_days) : 0,
                      amount: Number(values.amount),
                    })
                  }
                >
                  <Form.Item name="merchant_no" label="merchant_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="out_trade_no" label="out_trade_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="title" label="title"><Input /></Form.Item>
                  <Form.Item name="remark" label="remark"><Input.TextArea rows={3} /></Form.Item>
                  <Form.Item name="from_account_no" label="from_account_no"><Input /></Form.Item>
                  <Form.Item name="from_out_user_id" label="from_out_user_id"><Input /></Form.Item>
                  <Form.Item name="to_account_no" label="to_account_no"><Input /></Form.Item>
                  <Form.Item name="to_out_user_id" label="to_out_user_id"><Input /></Form.Item>
                  <Form.Item name="to_expire_in_days" label="to_expire_in_days"><Input /></Form.Item>
                  <Form.Item name="amount" label="amount" rules={[{ required: true }]}><Input /></Form.Item>
                  <Button loading={submitting} type="primary" htmlType="submit">提交</Button>
                </Form>
              ),
            },
            {
              key: "refund",
              label: "退款",
              children: (
                <Form
                  layout="vertical"
                  initialValues={{ merchant_no: merchantNo }}
                  onFinish={(values) =>
                    void submitTxn("/transactions/refund", {
                      merchant_no: values.merchant_no,
                      out_trade_no: values.out_trade_no,
                      title: values.title,
                      remark: values.remark,
                      refund_of_txn_no: values.refund_of_txn_no,
                      amount: Number(values.amount),
                    })
                  }
                >
                  <Form.Item name="merchant_no" label="merchant_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="out_trade_no" label="out_trade_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="title" label="title"><Input /></Form.Item>
                  <Form.Item name="remark" label="remark"><Input.TextArea rows={3} /></Form.Item>
                  <Form.Item name="refund_of_txn_no" label="refund_of_txn_no" rules={[{ required: true }]}><Input /></Form.Item>
                  <Form.Item name="amount" label="amount" rules={[{ required: true }]}><Input /></Form.Item>
                  <Button loading={submitting} type="primary" htmlType="submit">提交</Button>
                </Form>
              ),
            },
          ]}
        />
      </Modal>

      <Drawer title={`交易详情 ${detailTxnNo}`} open={detailOpen} width={760} onClose={() => setDetailOpen(false)}>
        <pre className="result-box">{detailJSON || "加载中..."}</pre>
      </Drawer>
    </AdminShell>
  );
}
