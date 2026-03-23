"use client";

import {
  Button,
  Card,
  Drawer,
  Form,
  Input,
  Modal,
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

type MerchantListItem = {
  merchant_no: string;
  name: string;
  budget_account_no: string;
  receivable_account_no: string;
};

type MerchantListResponse = {
  items: MerchantListItem[];
  next_page_token: string;
};

type MerchantDetail = {
  merchant_no: string;
  name: string;
  status: string;
  budget_account_no: string;
  receivable_account_no: string;
  secret_version: number;
  auto_create_account_on_customer_create: boolean;
  auto_create_customer_on_credit: boolean;
  webhook: {
    url: string;
    enabled: boolean;
  };
};

function formatAPIError(err: unknown): string {
  if (err instanceof APIError) {
    return `${err.code}: ${err.message}`;
  }
  return "request failed";
}

export default function MerchantsPage(): JSX.Element {
  const [rows, setRows] = useState<MerchantListItem[]>([]);
  const [nextToken, setNextToken] = useState("");
  const [pageTokenInput, setPageTokenInput] = useState("");
  const [pageSizeInput, setPageSizeInput] = useState("20");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [createOpen, setCreateOpen] = useState(false);
  const [createForm] = Form.useForm();

  const [detailOpen, setDetailOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detail, setDetail] = useState<MerchantDetail | null>(null);
  const [detailMerchantNo, setDetailMerchantNo] = useState("");

  const [featureForm] = Form.useForm();
  const [webhookForm] = Form.useForm();
  const [rotateSecret, setRotateSecret] = useState("");

  const pageSize = useMemo(() => {
    const v = Number(pageSizeInput);
    if (!Number.isFinite(v) || v <= 0) {
      return 20;
    }
    return Math.min(200, Math.trunc(v));
  }, [pageSizeInput]);

  const loadMerchants = async (token?: string): Promise<void> => {
    setLoading(true);
    setError("");
    try {
      const params = new URLSearchParams();
      params.set("page_size", String(pageSize));
      const cursor = (token ?? pageTokenInput).trim();
      if (cursor) {
        params.set("page_token", cursor);
      }
      const data = await apiRequest<MerchantListResponse>(`/merchants?${params.toString()}`);
      setRows(data.items ?? []);
      setNextToken(data.next_page_token ?? "");
    } catch (err) {
      setError(formatAPIError(err));
    } finally {
      setLoading(false);
    }
  };

  const loadDetail = async (merchantNo: string): Promise<void> => {
    setDetailLoading(true);
    try {
      const data = await apiRequest<MerchantDetail>(`/merchants/${encodeURIComponent(merchantNo)}`);
      setDetail(data);
      featureForm.setFieldsValue({
        auto_create_account_on_customer_create: data.auto_create_account_on_customer_create,
        auto_create_customer_on_credit: data.auto_create_customer_on_credit,
      });
      webhookForm.setFieldsValue({
        url: data.webhook?.url ?? "",
        enabled: Boolean(data.webhook?.enabled),
      });
    } catch (err) {
      message.error(formatAPIError(err));
    } finally {
      setDetailLoading(false);
    }
  };

  useEffect(() => {
    void loadMerchants("");
  }, []);

  const columns: ColumnsType<MerchantListItem> = [
    { title: "Merchant No", dataIndex: "merchant_no", key: "merchant_no", width: 190 },
    { title: "Name", dataIndex: "name", key: "name", width: 220 },
    { title: "Budget Account", dataIndex: "budget_account_no", key: "budget_account_no", width: 220, render: (v) => <Typography.Text code>{v}</Typography.Text> },
    { title: "Receivable Account", dataIndex: "receivable_account_no", key: "receivable_account_no", width: 220, render: (v) => <Typography.Text code>{v}</Typography.Text> },
    {
      title: "Action",
      key: "action",
      width: 160,
      render: (_, row) => (
        <Button
          onClick={() => {
            setDetailMerchantNo(row.merchant_no);
            setRotateSecret("");
            setDetailOpen(true);
            void loadDetail(row.merchant_no);
          }}
        >
          详情/操作
        </Button>
      ),
    },
  ];

  return (
    <AdminShell>
      <Typography.Title className="page-title">商户管理</Typography.Title>

      <Card style={{ marginBottom: 12 }}>
        <Space wrap>
          <Input
            style={{ width: 220 }}
            value={pageTokenInput}
            onChange={(e) => setPageTokenInput(e.target.value)}
            placeholder="page_token"
          />
          <Input
            style={{ width: 120 }}
            value={pageSizeInput}
            onChange={(e) => setPageSizeInput(e.target.value)}
            placeholder="page_size"
          />
          <Button type="primary" loading={loading} onClick={() => void loadMerchants()}>
            查询列表
          </Button>
          <Button
            disabled={!nextToken}
            onClick={() => {
              setPageTokenInput(nextToken);
              void loadMerchants(nextToken);
            }}
          >
            下一页
          </Button>
          <Button
            onClick={() => {
              createForm.resetFields();
              setCreateOpen(true);
            }}
          >
            新建商户
          </Button>
          {nextToken ? <Tag color="blue">next_token: {nextToken}</Tag> : <Tag>末页</Tag>}
        </Space>
        {error ? (
          <Typography.Text type="danger" style={{ display: "block", marginTop: 8 }}>
            {error}
          </Typography.Text>
        ) : null}
      </Card>

      <Card>
        <Table<MerchantListItem>
          rowKey="merchant_no"
          columns={columns}
          dataSource={rows}
          loading={loading}
          pagination={false}
          scroll={{ x: 980 }}
        />
      </Card>

      <Modal
        title="新建商户"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        footer={null}
        destroyOnClose
      >
        <Form
          form={createForm}
          layout="vertical"
          initialValues={{
            auto_create_account_on_customer_create: true,
            auto_create_customer_on_credit: true,
          }}
          onFinish={async (values) => {
            try {
              const data = await apiRequest<any>("/merchants", {
                method: "POST",
                body: JSON.stringify(values),
              });
              message.success("商户创建成功");
              Modal.info({
                title: "商户密钥（仅展示一次）",
                width: 780,
                content: <pre className="result-box">{JSON.stringify(data, null, 2)}</pre>,
              });
              setCreateOpen(false);
              await loadMerchants("");
            } catch (err) {
              message.error(formatAPIError(err));
            }
          }}
        >
          <Form.Item name="name" label="商户名称" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="auto_create_account_on_customer_create" label="开户自动建账户" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Form.Item name="auto_create_customer_on_credit" label="发币自动建客户" valuePropName="checked">
            <Switch />
          </Form.Item>
          <Button htmlType="submit" type="primary" block>
            创建
          </Button>
        </Form>
      </Modal>

      <Drawer
        title={`商户详情 ${detailMerchantNo}`}
        open={detailOpen}
        width={700}
        onClose={() => setDetailOpen(false)}
      >
        {detailLoading ? <Typography.Text>加载中...</Typography.Text> : null}
        {detail ? (
          <Space direction="vertical" style={{ width: "100%" }} size={16}>
            <Card size="small" title="概览">
              <pre className="result-box">{JSON.stringify(detail, null, 2)}</pre>
            </Card>

            <Card size="small" title="商户特性">
              <Form
                form={featureForm}
                layout="vertical"
                onFinish={async (values) => {
                  try {
                    await apiRequest(`/merchants/${encodeURIComponent(detail.merchant_no)}/features`, {
                      method: "PATCH",
                      body: JSON.stringify(values),
                    });
                    message.success("商户特性已更新");
                    await loadDetail(detail.merchant_no);
                  } catch (err) {
                    message.error(formatAPIError(err));
                  }
                }}
              >
                <Form.Item name="auto_create_account_on_customer_create" label="开户自动建账户" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Form.Item name="auto_create_customer_on_credit" label="发币自动建客户" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Button htmlType="submit">保存特性</Button>
              </Form>
            </Card>

            <Card size="small" title="Webhook 配置">
              <Form
                form={webhookForm}
                layout="vertical"
                onFinish={async (values) => {
                  try {
                    await apiRequest(`/merchants/${encodeURIComponent(detail.merchant_no)}/webhooks/config`, {
                      method: "PUT",
                      body: JSON.stringify(values),
                    });
                    message.success("Webhook 已保存");
                    await loadDetail(detail.merchant_no);
                  } catch (err) {
                    message.error(formatAPIError(err));
                  }
                }}
              >
                <Form.Item name="url" label="Webhook URL" rules={[{ required: true }]}>
                  <Input placeholder="https://example.com/webhook" />
                </Form.Item>
                <Form.Item name="enabled" label="启用" valuePropName="checked">
                  <Switch />
                </Form.Item>
                <Button htmlType="submit">保存 Webhook</Button>
              </Form>
            </Card>

            <Card size="small" title="敏感操作">
              <Space direction="vertical" style={{ width: "100%" }}>
                <Button
                  danger
                  onClick={async () => {
                    try {
                      const data = await apiRequest<{ merchant_secret: string; secret_version: number }>(
                        `/merchants/${encodeURIComponent(detail.merchant_no)}/secret:rotate`,
                        { method: "POST", body: JSON.stringify({}) }
                      );
                      setRotateSecret(JSON.stringify(data, null, 2));
                      message.success("密钥轮转成功");
                    } catch (err) {
                      message.error(formatAPIError(err));
                    }
                  }}
                >
                  轮转商户密钥
                </Button>
                {rotateSecret ? <pre className="result-box">{rotateSecret}</pre> : null}
              </Space>
            </Card>
          </Space>
        ) : null}
      </Drawer>
    </AdminShell>
  );
}
