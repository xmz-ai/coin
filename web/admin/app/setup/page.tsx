"use client";

import { Alert, Button, Card, Form, Input, Typography } from "antd";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { getAccessToken } from "../../lib/auth";
import { APIError, apiRequest } from "../../lib/api";
import { SetupInitializeResponse, clearSetupResult, saveSetupResult } from "../../lib/setup";

type SetupStatusResponse = {
  initialized: boolean;
};

export default function SetupPage(): JSX.Element {
  const router = useRouter();
  const [booting, setBooting] = useState(true);
  const [loading, setLoading] = useState(false);
  const [errMsg, setErrMsg] = useState("");

  useEffect(() => {
    let active = true;
    const check = async (): Promise<void> => {
      if (getAccessToken()) {
        router.replace("/dashboard");
        return;
      }
      try {
        const status = await apiRequest<SetupStatusResponse>("/setup/status", {}, { auth: false });
        if (!active) {
          return;
        }
        if (status.initialized) {
          clearSetupResult();
          router.replace("/login");
          return;
        }
      } catch {
      }
      if (active) {
        setBooting(false);
      }
    };
    void check();
    return () => {
      active = false;
    };
  }, [router]);

  if (booting) {
    return <div className="shell-loading">正在检查首次初始化状态...</div>;
  }

  return (
    <div className="login-wrap">
      <Card className="login-card setup-card" bordered={false}>
        <Typography.Title level={3} style={{ marginTop: 0 }}>
          首次启动引导
        </Typography.Title>
        <Typography.Paragraph type="secondary">
          初始化管理员账号和默认商户。初始化成功后，请妥善保管商户密钥。
        </Typography.Paragraph>
        {errMsg ? <Alert type="error" message={errMsg} style={{ marginBottom: 12 }} /> : null}
        <Form
          layout="vertical"
          initialValues={{ merchant_name: "Default Merchant" }}
          onFinish={async (values: {
            admin_username: string;
            admin_password: string;
            admin_password_confirm: string;
            merchant_name: string;
            merchant_webhook_url?: string;
          }) => {
            setLoading(true);
            setErrMsg("");
            try {
              const data = await apiRequest<SetupInitializeResponse>(
                "/setup/initialize",
                {
                  method: "POST",
                  body: JSON.stringify({
                    admin_username: values.admin_username,
                    admin_password: values.admin_password,
                    merchant_name: values.merchant_name,
                    merchant_webhook_url: values.merchant_webhook_url ?? "",
                  }),
                },
                { auth: false }
              );
              saveSetupResult(data);
              router.replace("/setup/success");
            } catch (err) {
              if (err instanceof APIError) {
                setErrMsg(`${err.code}: ${err.message}`);
              } else {
                setErrMsg("setup failed");
              }
            } finally {
              setLoading(false);
            }
          }}
        >
          <Form.Item
            label="管理员用户名"
            name="admin_username"
            rules={[
              { required: true },
              { min: 3, max: 64 },
              { pattern: /^[A-Za-z0-9_.-]+$/, message: "仅支持字母、数字、_ . -" },
            ]}
          >
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item label="管理员密码" name="admin_password" rules={[{ required: true }, { min: 8, max: 72 }]}>
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            label="确认密码"
            name="admin_password_confirm"
            dependencies={["admin_password"]}
            rules={[
              { required: true },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  if (!value || getFieldValue("admin_password") === value) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error("两次输入的密码不一致"));
                },
              }),
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item label="默认商户名称" name="merchant_name" rules={[{ required: true }, { max: 128 }]}>
            <Input />
          </Form.Item>
          <Form.Item
            label="默认商户回调地址（可选）"
            name="merchant_webhook_url"
            rules={[
              {
                validator(_, value) {
                  const v = typeof value === "string" ? value.trim() : "";
                  if (!v || v.toLowerCase().startsWith("https://")) {
                    return Promise.resolve();
                  }
                  return Promise.reject(new Error("仅支持 https:// 开头"));
                },
              },
            ]}
            extra="留空则默认不回调，交易成功也不会写入 outbox。"
          >
            <Input placeholder="https://example.com/webhook" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            初始化
          </Button>
        </Form>
      </Card>
    </div>
  );
}
