"use client";

import { Button, Card, Form, Input, Modal, Space, Table, Tag, Typography, message } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useMemo, useState } from "react";
import { AdminShell } from "../../components/AdminShell";
import { APIError, apiRequest } from "../../lib/api";

type CustomerItem = {
  customer_no: string;
  merchant_no: string;
  out_user_id: string;
  default_account_no: string;
  status: string;
  created_at: string;
};

type CustomerListResponse = {
  items: CustomerItem[];
  next_page_token: string;
};

function formatAPIError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return "request failed";
}

export default function CustomersPage(): JSX.Element {
  const [merchantNo, setMerchantNo] = useState("");
  const [outUserID, setOutUserID] = useState("");
  const [customerNo, setCustomerNo] = useState("");
  const [pageToken, setPageToken] = useState("");
  const [pageSizeInput, setPageSizeInput] = useState("20");

  const [rows, setRows] = useState<CustomerItem[]>([]);
  const [nextToken, setNextToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [createOpen, setCreateOpen] = useState(false);
  const [createForm] = Form.useForm();

  const pageSize = useMemo(() => {
    const v = Number(pageSizeInput);
    if (!Number.isFinite(v) || v <= 0) {
      return 20;
    }
    return Math.min(200, Math.trunc(v));
  }, [pageSizeInput]);

  const loadCustomers = async (cursor?: string): Promise<void> => {
    setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams();
      params.set("merchant_no", merchantNo.trim());
      params.set("page_size", String(pageSize));
      if (outUserID.trim()) {
        params.set("out_user_id", outUserID.trim());
      }
      if (customerNo.trim()) {
        params.set("customer_no", customerNo.trim());
      }
      const token = (cursor ?? pageToken).trim();
      if (token) {
        params.set("page_token", token);
      }
      const data = await apiRequest<CustomerListResponse>(`/customers?${params.toString()}`);
      setRows(data.items ?? []);
      setNextToken(data.next_page_token ?? "");
    } catch (err) {
      setError(formatAPIError(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadCustomers("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const columns: ColumnsType<CustomerItem> = [
    { title: "Customer No", dataIndex: "customer_no", key: "customer_no", width: 180 },
    { title: "Out User ID", dataIndex: "out_user_id", key: "out_user_id", width: 220 },
    { title: "Default Account", dataIndex: "default_account_no", key: "default_account_no", width: 220 },
    { title: "Status", dataIndex: "status", key: "status", width: 120, render: (v) => <Tag color="green">{v}</Tag> },
    { title: "Created At", dataIndex: "created_at", key: "created_at", width: 220 },
  ];

  return (
    <AdminShell>
      <Typography.Title className="page-title">客户管理</Typography.Title>

      <Card style={{ marginBottom: 12 }}>
        <Space wrap>
          <Input style={{ width: 200 }} value={merchantNo} onChange={(e) => setMerchantNo(e.target.value)} placeholder="merchant_no" />
          <Input style={{ width: 200 }} value={outUserID} onChange={(e) => setOutUserID(e.target.value)} placeholder="out_user_id" />
          <Input style={{ width: 180 }} value={customerNo} onChange={(e) => setCustomerNo(e.target.value)} placeholder="customer_no" />
          <Input style={{ width: 170 }} value={pageToken} onChange={(e) => setPageToken(e.target.value)} placeholder="page_token" />
          <Input style={{ width: 120 }} value={pageSizeInput} onChange={(e) => setPageSizeInput(e.target.value)} placeholder="page_size" />
          <Button type="primary" loading={loading} onClick={() => void loadCustomers()}>
            查询列表
          </Button>
          <Button
            disabled={!nextToken}
            onClick={() => {
              setPageToken(nextToken);
              void loadCustomers(nextToken);
            }}
          >
            下一页
          </Button>
          <Button
            onClick={() => {
              createForm.setFieldsValue({ merchant_no: merchantNo.trim() });
              setCreateOpen(true);
            }}
          >
            新建客户
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
        <Table<CustomerItem>
          rowKey="customer_no"
          columns={columns}
          dataSource={rows}
          loading={loading}
          pagination={false}
          scroll={{ x: 980 }}
        />
      </Card>

      <Modal title="新建客户" open={createOpen} onCancel={() => setCreateOpen(false)} footer={null} destroyOnClose>
        <Form
          form={createForm}
          layout="vertical"
          onFinish={async (values: { merchant_no: string; out_user_id: string }) => {
            try {
              await apiRequest("/customers", { method: "POST", body: JSON.stringify(values) });
              message.success("客户创建成功");
              setCreateOpen(false);
              setMerchantNo(values.merchant_no ?? merchantNo);
              setOutUserID(values.out_user_id ?? "");
              setPageToken("");
              await loadCustomers("");
            } catch (err) {
              message.error(formatAPIError(err));
            }
          }}
        >
          <Form.Item name="merchant_no" label="merchant_no" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="out_user_id" label="out_user_id" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Button type="primary" htmlType="submit" block>
            创建
          </Button>
        </Form>
      </Modal>
    </AdminShell>
  );
}
