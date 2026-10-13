#Requires -Version 5.1
<#
.SYNOPSIS
Sends a set of sample webhooks to a Relaybox endpoint.

.DESCRIPTION
The PowerShell version of send-samples.sh: JSON, form, multipart, XML, plain
text, a GET with a query string, and a DELETE. All data is made up, and some
of it is meant to be redacted.

.EXAMPLE
.\examples\curl\send-samples.ps1 http://127.0.0.1:8787/hooks/<public_id>
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0, HelpMessage = 'Endpoint URL, for example http://127.0.0.1:8787/hooks/<public_id>')]
    [string] $Url
)

$ErrorActionPreference = 'Stop'
$Url = $Url.TrimEnd('/')
$payloads = Join-Path $PSScriptRoot 'payloads'

function Send-Sample {
    param([string] $Label, [hashtable] $Request)
    $status = try {
        (Invoke-WebRequest -UseBasicParsing @Request).StatusCode
    }
    catch {
        if ($_.Exception.Response) { [int]$_.Exception.Response.StatusCode } else { "failed: $($_.Exception.Message)" }
    }
    Write-Host ('{0,-32}{1}' -f $Label, $status)
}

Send-Sample 'JSON payment event' @{
    Method = 'Post'; Uri = "$Url/payments?attempt=1"
    ContentType = 'application/json'; UserAgent = 'ExamplePayments/2.1'
    Headers = @{
        'Webhook-Id'        = 'evt_example_0001'
        'Webhook-Signature' = 't=1760090000,v1=3f1c8a0d9b7e6c5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c8b7a6f5e4d3c2b1a'
    }
    InFile = Join-Path $payloads 'payment-succeeded.json'
}

Send-Sample 'Push event' @{
    Method = 'Post'; Uri = "$Url/git"
    ContentType = 'application/json'; UserAgent = 'ExampleHookshot/1.0'
    Headers = @{
        'X-GitHub-Event'      = 'push'
        'X-GitHub-Delivery'   = '72d3162e-cc78-11e3-81ab-4c9367dc0958'
        'X-Hub-Signature-256' = 'sha256=7d38cdd689735b008b3c702edd92eea23791c5f6a3c4c1f2b5d8a9e0f1b2c3d4'
    }
    InFile = Join-Path $payloads 'push.json'
}

Send-Sample 'Form status callback' @{
    Method = 'Post'; Uri = "$Url/sms/status"
    ContentType = 'application/x-www-form-urlencoded'
    InFile = Join-Path $payloads 'status-callback.txt'
}

$boundary = 'relaybox-example-' + [guid]::NewGuid().ToString('N')
$receipt = [IO.File]::ReadAllText((Join-Path $payloads 'receipt.txt'))
$multipart = @(
    "--$boundary", 'Content-Disposition: form-data; name="order_id"', '', 'ord_1042',
    "--$boundary", 'Content-Disposition: form-data; name="password"', '', 'example-password',
    "--$boundary", 'Content-Disposition: form-data; name="receipt"; filename="receipt.txt"', 'Content-Type: text/plain', '', $receipt,
    "--$boundary--", ''
) -join "`r`n"
Send-Sample 'Multipart upload' @{
    Method = 'Post'; Uri = "$Url/uploads"
    ContentType = "multipart/form-data; boundary=$boundary"
    Body = [Text.Encoding]::UTF8.GetBytes($multipart)
}

Send-Sample 'XML notification' @{
    Method = 'Post'; Uri = "$Url/invoices"
    ContentType = 'application/xml'
    InFile = Join-Path $payloads 'invoice.xml'
}

Send-Sample 'Plain text with a bearer token' @{
    Method = 'Put'; Uri = "$Url/orders/ord_1042/status"
    ContentType = 'text/plain'
    Headers = @{ Authorization = 'Bearer example-token-abc123' }
    Body = 'shipped'
}

Send-Sample 'GET with an API key' @{
    Method = 'Get'; Uri = "$Url/ping?event=ping&api_key=example-key-123&page=2"
}

Send-Sample 'DELETE without a body' @{
    Method = 'Delete'; Uri = "$Url/subscriptions/sub_example_9"
    Headers = @{ 'X-Api-Key' = 'example-key-456' }
}

Write-Host 'Done. Open the dashboard to inspect the requests.'
