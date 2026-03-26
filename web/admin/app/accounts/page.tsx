"use client";

import {
  Button,
  Card,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useMemo, useState } from "react";
import { AdminShell } from "../../components/AdminShell";
import { APIError, apiRequest } from "../../lib/api";

type AccountItem = {
  account_no: string;
  merchant_no: string;
  customer_no: string;
  owner_out_user_id: string;
  account_type: string;
  allow_overdraft: boolean;
  max_overdraft_limit: number;
  allow_debit_out: boolean;
  allow_credit_in: boolean;
  allow_transfer: boolean;
  book_enabled: boolean;
  balance: number;
  created_at: string;
};

type AccountListResponse = {
  items: AccountItem[];
  next_page_token: string;
};

type AccountBalance = {
  account_no: string;
  merchant_no: string;
  balance: number;
  available_balance: number;
  book_enabled: boolean;
  book_balance_sum: number;
};

function formatAPIError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return "request failed";
}

export default function AccountsPage(): JSX.Element {
  const [merchantNo, setMerchantNo] = useState("");
  const [outUserID, setOutUserID] = useState("");
  const [customerNo, setCustomerNo] = useState("");
  const [accountNoFilter, setAccountNoFilter] = useState("");
  const [pageToken, setPageToken] = useState("");
  const [pageSizeInput, setPageSizeInput] = useState("20");

  const [rows, setRows] = useState<AccountItem[]>([]);
  const [nextToken, setNextToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [createOpen, setCreateOpen] = useState(false);
  const [createForm] = Form.useForm();
  const [capOpen, setCapOpen] = useState(false);
  const [capForm] = Form.useForm();
  const [activeAccount, setActiveAccount] = useState<AccountItem | null>(null);
  const [balanceView, setBalanceView] = useState<AccountBalance | null>(null);

  const pageSize = useMemo(() => {
    const v = Number(pageSizeInput);
    if (!Number.isFinite(v) || v <= 0) {
      return 20;
    }
    return Math.min(200, Math.trunc(v));
  }, [pageSizeInput]);

  const loadAccounts = async (cursor?: string): Promise<void> => {
    setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams();
      params.set("merchant_no", merchantNo.trim());
      params.set("page_size", String(pageSize));
      if (outUserID.trim()) params.set("out_user_id", outUserID.trim());
      if (customerNo.trim()) params.set("customer_no", customerNo.trim());
      if (accountNoFilter.trim()) params.set("account_no", accountNoFilter.trim());
      const token = (cursor ?? pageToken).trim();
      if (token) params.set("page_token", token);
      const data = await apiRequest<AccountListResponse>(`/accounts?${params.toString()}`);
      setRows(data.items ?? []);
      setNextToken(data.next_page_token ?? "");
    } catch (err) {
      setError(formatAPIError(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadAccounts("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const columns: ColumnsType<AccountItem> = [
    { title: "Account No", dataIndex: "account_no", key: "account_no", width: 200 },
    { title: "Type", dataIndex: "account_type", key: "account_type", width: 120 },
    {
      title: "Owner",
      key: "owner",
      width: 260,
      render: (_, row) => (
        <Space direction="vertical" size={0}>
          <Typography.Text code>{row.customer_no || "-"}</Typography.Text>
          <Typography.Text type="secondary">{row.owner_out_user_id || "-"}</Typography.Text>
        </Space>
      ),
    },
    { title: "Balance", dataIndex: "balance", key: "balance", width: 130 },
    {
      title: "Book",
      dataIndex: "book_enabled",
      key: "book_enabled",
      width: 100,
      render: (v) => (v ? <Tag color="blue">ON</Tag> : <Tag>OFF</Tag>),
    },
    {
      title: "Capability",
      key: "capability",
      render: (_, row) => (
        <Space size={4} wrap>
          <Tag color={row.allow_debit_out ? "green" : "red"}>debit</Tag>
          <Tag color={row.allow_credit_in ? "green" : "red"}>credit</Tag>
          <Tag color={row.allow_transfer ? "green" : "red"}>transfer</Tag>
        </Space>
      ),
    },
    {
      title: "Action",
      key: "action",
      width: 210,
      render: (_, row) => (
        <Space>
          <Button
            onClick={async () => {
              try {
                const data = await apiRequest<AccountBalance>(`/accounts/${encodeURIComponent(row.account_no)}/balance`);
                setBalanceView(data);
              } catch (err) {
                message.error(formatAPIError(err));
              }
            }}
          >
            余额
          </Button>
          <Button
            onClick={() => {
              setActiveAccount(row);
              capForm.setFieldsValue({
                allow_transfer: row.allow_transfer,
                allow_credit_in: row.allow_credit_in,
                allow_debit_out: row.allow_debit_out,
              });
              setCapOpen(true);
            }}
          >
            能力
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <AdminShell>
      <Typography.Title className="page-title">账户管理</Typography.Title>

      <Card style={{ marginBottom: 12 }}>
        <Space wrap>
          <Input style={{ width: 200 }} value={merchantNo} onChange={(e) => setMerchantNo(e.target.value)} placeholder="merchant_no" />
          <Input style={{ width: 180 }} value={outUserID} onChange={(e) => setOutUserID(e.target.value)} placeholder="out_user_id" />
          <Input style={{ width: 180 }} value={customerNo} onChange={(e) => setCustomerNo(e.target.value)} placeholder="customer_no" />
          <Input style={{ width: 180 }} value={accountNoFilter} onChange={(e) => setAccountNoFilter(e.target.value)} placeholder="account_no" />
          <Input style={{ width: 170 }} value={pageToken} onChange={(e) => setPageToken(e.target.value)} placeholder="page_token" />
          <Input style={{ width: 120 }} value={pageSizeInput} onChange={(e) => setPageSizeInput(e.target.value)} placeholder="page_size" />
          <Button type="primary" loading={loading} onClick={() => void loadAccounts()}>
            查询列表
          </Button>
          <Button
            disabled={!nextToken}
            onClick={() => {
              setPageToken(nextToken);
              void loadAccounts(nextToken);
            }}
          >
            下一页
          </Button>
          <Button
            onClick={() => {
              createForm.setFieldsValue({ merchant_no: merchantNo.trim(), owner_type: "CUSTOMER" });
              setCreateOpen(true);
            }}
          >
            新建账户
          </Button>
          {nextToken ? <Tag color="blue">next_token: {nextToken}</Tag> : <Tag>末页</Tag>}
        </Space>
        {error ? (
          <Typography.Text type="danger" style={{ marginTop: 8, display: "block" }}>
            {error}
          </Typography.Text>
        ) : null}
      </Card>

      <Card>
        <Table<AccountItem>
          rowKey="account_no"
          columns={columns}
          dataSource={rows}
          loading={loading}
          pagination={false}
          scroll={{ x: 1200 }}
        />
        {balanceView ? (
          <Card size="small" title={`余额详情 ${balanceView.account_no}`} style={{ marginTop: 16 }}>
            <Space wrap size={[24, 12]}>
              <Typography.Text>账面余额: {balanceView.balance}</Typography.Text>
              <Typography.Text>可用余额: {balanceView.available_balance}</Typography.Text>
              <Typography.Text>账本汇总: {balanceView.book_balance_sum}</Typography.Text>
              <Typography.Text>账本模式: {balanceView.book_enabled ? "ON" : "OFF"}</Typography.Text>
            </Space>
            <pre className="result-box">{JSON.stringify(balanceView, null, 2)}</pre>
          </Card>
        ) : null}
      </Card>

      <Modal title="新建账户" open={createOpen} onCancel={() => setCreateOpen(false)} footer={null} destroyOnClose width={760}>
        <Form
          form={createForm}
          layout="vertical"
          initialValues={{
            owner_type: "CUSTOMER",
            allow_transfer: true,
            allow_credit_in: true,
            allow_debit_out: true,
            allow_overdraft: false,
            book_enabled: false,
          }}
          onFinish={async (values) => {
            try {
              await apiRequest("/accounts", {
                method: "POST",
                body: JSON.stringify({
                  merchant_no: values.merchant_no,
                  owner_type: values.owner_type,
                  owner_out_user_id: values.owner_out_user_id,
                  owner_customer_no: values.owner_customer_no,
                  account_type: values.account_type,
                  capability: {
                    allow_overdraft: values.allow_overdraft,
                    max_overdraft_limit: values.max_overdraft_limit ? Number(values.max_overdraft_limit) : 0,
                    allow_transfer: values.allow_transfer,
                    allow_credit_in: values.allow_credit_in,
                    allow_debit_out: values.allow_debit_out,
                    book_enabled: values.book_enabled,
                  },
                }),
              });
              message.success("账户创建成功");
              setCreateOpen(false);
              await loadAccounts("");
            } catch (err) {
              message.error(formatAPIError(err));
            }
          }}
        >
          <div className="page-grid">
            <Form.Item className="page-card half" name="merchant_no" label="merchant_no" rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Form.Item className="page-card half" name="owner_type" label="owner_type" rules={[{ required: true }]}>
              <Select options={[{ value: "CUSTOMER" }, { value: "MERCHANT" }]} />
            </Form.Item>
            <Form.Item className="page-card half" name="owner_out_user_id" label="owner_out_user_id">
              <Input />
            </Form.Item>
            <Form.Item className="page-card half" name="owner_customer_no" label="owner_customer_no">
              <Input />
            </Form.Item>
            <Form.Item className="page-card half" name="account_type" label="account_type">
              <Input placeholder="CUSTOMER / MERCHANT / CUSTOM" />
            </Form.Item>
            <Form.Item className="page-card half" name="max_overdraft_limit" label="max_overdraft_limit">
              <Input />
            </Form.Item>
          </div>

          <Space wrap>
            <Form.Item name="allow_overdraft" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
            <span>allow_overdraft</span>
            <Form.Item name="allow_transfer" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
            <span>allow_transfer</span>
            <Form.Item name="allow_credit_in" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
            <span>allow_credit_in</span>
            <Form.Item name="allow_debit_out" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
            <span>allow_debit_out</span>
            <Form.Item name="book_enabled" valuePropName="checked" noStyle>
              <Switch />
            </Form.Item>
            <span>book_enabled</span>
          </Space>

          <Button style={{ marginTop: 14 }} type="primary" htmlType="submit" block>
            创建
          </Button>
        </Form>
      </Modal>

      <Modal title={`更新账户能力 ${activeAccount?.account_no ?? ""}`} open={capOpen} onCancel={() => setCapOpen(false)} footer={null} destroyOnClose>
        <Form
          form={capForm}
          layout="vertical"
          onFinish={async (values) => {
            if (!activeAccount) return;
            try {
              await apiRequest(`/accounts/${encodeURIComponent(activeAccount.account_no)}/capability`, {
                method: "PATCH",
                body: JSON.stringify(values),
              });
              message.success("账户能力已更新");
              setCapOpen(false);
              await loadAccounts();
            } catch (err) {
              message.error(formatAPIError(err));
            }
          }}
        >
          <Form.Item name="allow_transfer" label="allow_transfer" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="allow_credit_in" label="allow_credit_in" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="allow_debit_out" label="allow_debit_out" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Button type="primary" htmlType="submit" block>
            保存
          </Button>
        </Form>
      </Modal>
    </AdminShell>
  );
}
