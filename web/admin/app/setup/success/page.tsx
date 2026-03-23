"use client";

import { CheckCircleFilled, CopyOutlined, EyeInvisibleOutlined, EyeOutlined } from "@ant-design/icons";
import { Alert, Button, Card, Space, Typography, message } from "antd";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { getAccessToken } from "../../../lib/auth";
import { SetupInitializeResponse, clearSetupResult, getSetupResult } from "../../../lib/setup";

function buildCredentialText(data: SetupInitializeResponse): string {
  return [
    `admin_username=${data.admin_username}`,
    `merchant_no=${data.merchant_no}`,
    `merchant_secret=${data.merchant_secret}`,
    `secret_version=${data.secret_version}`,
    `budget_account_no=${data.budget_account_no}`,
    `receivable_account_no=${data.receivable_account_no}`,
  ].join("\n");
}

async function copyText(content: string, okMessage: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(content);
    message.success(okMessage);
  } catch {
    message.error("复制失败，请手动复制");
  }
}

type CredentialRowProps = {
  label: string;
  value: string;
  onCopy: () => void;
  secret?: boolean;
};

function CredentialRow({ label, value, onCopy, secret = false }: CredentialRowProps): JSX.Element {
  const [showSecret, setShowSecret] = useState(false);
  const displayValue = secret && !showSecret ? "••••••••••••••••••••••••••••••••" : value;

  return (
    <div className="setup-credential-row">
      <div className="setup-credential-main">
        <Typography.Text className="setup-credential-label">{label}</Typography.Text>
        <Typography.Text code className={secret ? "setup-credential-secret" : "setup-credential-value"}>
          {displayValue}
        </Typography.Text>
      </div>
      <Space>
        {secret ? (
          <Button
            size="small"
            icon={showSecret ? <EyeInvisibleOutlined /> : <EyeOutlined />}
            onClick={() => setShowSecret((v) => !v)}
          />
        ) : null}
        <Button size="small" icon={<CopyOutlined />} onClick={onCopy}>
          复制
        </Button>
      </Space>
    </div>
  );
}

export default function SetupSuccessPage(): JSX.Element {
  const router = useRouter();
  const [ready, setReady] = useState(false);
  const [data, setData] = useState<SetupInitializeResponse | null>(null);

  useEffect(() => {
    if (getAccessToken()) {
      router.replace("/dashboard");
      return;
    }
    const result = getSetupResult();
    if (!result) {
      router.replace("/login");
      return;
    }
    setData(result);
    setReady(true);
  }, [router]);

  const credentialText = useMemo(() => (data ? buildCredentialText(data) : ""), [data]);

  if (!ready || !data) {
    return <div className="shell-loading">正在加载初始化结果...</div>;
  }

  return (
    <div className="login-wrap">
      <Card className="login-card setup-success-card" bordered={false}>
        <div className="setup-success-header">
          <CheckCircleFilled className="setup-success-icon" />
          <div>
            <Typography.Title level={3} style={{ margin: 0 }}>
              初始化完成
            </Typography.Title>
            <Typography.Paragraph type="secondary" style={{ margin: "6px 0 0" }}>
              请先保存以下凭证，再进行登录或后续接入。
            </Typography.Paragraph>
          </div>
        </div>

        <Alert
          type="warning"
          showIcon
          message="商户密钥请立即保存到安全位置，丢失后需在商户管理里轮换新密钥。"
          style={{ marginBottom: 14 }}
        />

        <div className="setup-credential-list">
          <CredentialRow
            label="管理员"
            value={data.admin_username}
            onCopy={() => void copyText(data.admin_username, "管理员用户名已复制")}
          />
          <CredentialRow
            label="商户号"
            value={data.merchant_no}
            onCopy={() => void copyText(data.merchant_no, "商户号已复制")}
          />
          <CredentialRow
            label="商户密钥"
            value={data.merchant_secret}
            secret
            onCopy={() => void copyText(data.merchant_secret, "商户密钥已复制")}
          />
          <CredentialRow
            label="密钥版本"
            value={String(data.secret_version)}
            onCopy={() => void copyText(String(data.secret_version), "密钥版本已复制")}
          />
          <CredentialRow
            label="预算账户"
            value={data.budget_account_no}
            onCopy={() => void copyText(data.budget_account_no, "预算账户已复制")}
          />
          <CredentialRow
            label="应收账户"
            value={data.receivable_account_no}
            onCopy={() => void copyText(data.receivable_account_no, "应收账户已复制")}
          />
        </div>

        <div className="setup-success-actions">
          <Button icon={<CopyOutlined />} onClick={() => void copyText(credentialText, "凭证信息已复制")}>
            复制全部凭证
          </Button>
          <Button
            type="primary"
            onClick={() => {
              clearSetupResult();
              router.replace("/login");
            }}
          >
            已保存，去登录
          </Button>
        </div>
      </Card>
    </div>
  );
}
