"use client";

import { Button, Card, Col, Input, Row, Statistic, Typography } from "antd";
import { useEffect, useState } from "react";
import { AdminShell } from "../../components/AdminShell";
import { APIError, apiRequest } from "../../lib/api";

type DashboardData = {
  merchant_count: number;
  customer_count: number;
  account_count: number;
  txn_count: number;
  txn_status: Record<string, number>;
  outbox: Record<string, number>;
};

export default function DashboardPage(): JSX.Element {
  const [merchantNo, setMerchantNo] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [data, setData] = useState<DashboardData | null>(null);

  const load = async (): Promise<void> => {
    setLoading(true);
    setError("");
    try {
      const qs = merchantNo.trim() ? `?merchant_no=${encodeURIComponent(merchantNo.trim())}` : "";
      const res = await apiRequest<DashboardData>(`/dashboard/overview${qs}`);
      setData(res);
    } catch (err) {
      if (err instanceof APIError) {
        setError(`${err.code}: ${err.message}`);
      } else {
        setError("load dashboard failed");
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  return (
    <AdminShell>
      <Typography.Title className="page-title">运营概览</Typography.Title>
      <Card style={{ marginBottom: 14 }}>
        <Row gutter={10} align="middle">
          <Col xs={24} md={10}>
            <Input
              placeholder="按 merchant_no 过滤(可选)"
              value={merchantNo}
              onChange={(e) => setMerchantNo(e.target.value)}
            />
          </Col>
          <Col>
            <Button type="primary" onClick={() => void load()} loading={loading}>
              刷新
            </Button>
          </Col>
        </Row>
        {error ? (
          <Typography.Text type="danger" style={{ marginTop: 12, display: "block" }}>
            {error}
          </Typography.Text>
        ) : null}
      </Card>

      <Row gutter={[12, 12]}>
        <Col xs={12} md={6}><Card><Statistic title="商户" value={data?.merchant_count ?? 0} /></Card></Col>
        <Col xs={12} md={6}><Card><Statistic title="客户" value={data?.customer_count ?? 0} /></Card></Col>
        <Col xs={12} md={6}><Card><Statistic title="账户" value={data?.account_count ?? 0} /></Card></Col>
        <Col xs={12} md={6}><Card><Statistic title="交易总数" value={data?.txn_count ?? 0} /></Card></Col>
      </Row>

      <Row gutter={[12, 12]} style={{ marginTop: 8 }}>
        <Col xs={24} md={12}>
          <Card title="交易状态">
            <pre className="result-box">{JSON.stringify(data?.txn_status ?? {}, null, 2)}</pre>
          </Card>
        </Col>
        <Col xs={24} md={12}>
          <Card title="Outbox 状态">
            <pre className="result-box">{JSON.stringify(data?.outbox ?? {}, null, 2)}</pre>
          </Card>
        </Col>
      </Row>
    </AdminShell>
  );
}
