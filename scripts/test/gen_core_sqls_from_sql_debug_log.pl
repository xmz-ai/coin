#!/usr/bin/env perl
use strict;
use warnings;
use utf8;

my $input_log = $ARGV[0] // '/tmp/perf_sql_debug.log';
my $output_md = $ARGV[1] // 'core_sqls.md';

open my $in, '<', $input_log or die "open input log failed: $input_log: $!";
open my $out, '>:encoding(UTF-8)', $output_md or die "open output markdown failed: $output_md: $!";

my @order = qw(transfer refund book_transfer book_refund book_to_book_transfer book_to_book_refund);
my %label = (
	transfer              => '无 book 转账（transfer）',
	refund                => '无 book 退款（refund）',
	book_transfer         => '有 book 转账（book_transfer）',
	book_refund           => '有 book 退款（book_refund）',
	book_to_book_transfer => 'book to book 转账（book_to_book_transfer）',
	book_to_book_refund   => 'book to book 退款（book_to_book_refund）',
);

my %rows;
my %first_txn;

while (my $line = <$in>) {
	next unless $line =~ /\[sql-debug\] scenario=/;
	next if $line =~ /\[sql-debug\]\[summary\]/;

	my ($scenario) = $line =~ /\bscenario=([^\s]+)/;
	next unless defined $scenario;

	my ($elapsed) = $line =~ /\belapsed_ms=([^\s]+)/;
	my ($slow) = $line =~ /\bslow=([^\s]+)/;
	my ($query) = $line =~ /\bquery=([^\s]+)/;
	my ($txn_no) = $line =~ /\btxn_no=([^\s]+)/;

	$elapsed //= '';
	$slow //= '';
	$query //= '';

	my $sql = '';
	if ($line =~ /\ssql=\"(.*)\"\s*$/) {
		$sql = $1;
	}

	$query =~ s/\|/\\|/g;
	$sql =~ s/\|/\\|/g;

	push @{ $rows{$scenario} }, {
		query   => $query,
		elapsed => $elapsed,
		slow    => $slow,
		sql     => $sql,
	};

	if (!defined $first_txn{$scenario} && defined $txn_no && $txn_no ne '') {
		$first_txn{$scenario} = $txn_no;
	}
}

print {$out} "# Core SQLs\n\n";
print {$out} "采样来源：`$input_log`（`PERF_REQUESTS=1`，按场景染色）\n\n";

for my $scenario (@order) {
	my $name = $label{$scenario} // $scenario;
	print {$out} "## $name\n\n";
	if (defined $first_txn{$scenario} && $first_txn{$scenario} ne '') {
		print {$out} "- `txn_no`: `$first_txn{$scenario}`\n\n";
	}

	print {$out} "| # | Query | elapsed_ms | slow | SQL |\n";
	print {$out} "| ---: | --- | ---: | :---: | --- |\n";

	my $items = $rows{$scenario} || [];
	if (!@$items) {
		print {$out} "| 1 | `N/A` | 0 | false | (no sampled sql-debug lines) |\n\n";
		next;
	}

	my $idx = 1;
	for my $r (@$items) {
		print {$out} "| $idx | `$r->{query}` | $r->{elapsed} | $r->{slow} | $r->{sql} |\n";
		$idx++;
	}
	print {$out} "\n";
}

close $in;
close $out;
