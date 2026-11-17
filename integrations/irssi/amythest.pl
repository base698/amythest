use strict;
use warnings;
use Irssi;
use Irssi::Irc;
our $VERSION = '0.1';
our %IRSSI = (name => 'amythest', description => 'Focus Amythest conversations and selected-message context', license => 'MIT');

# Opt into focus on this named network only. Message content never becomes an
# Irssi command; channel names are the gateway's fixed hexadecimal identifiers.
Irssi::settings_add_str('amythest', 'amythest_server_tag', 'amy-messages');
sub focus_message {
    my ($server, $data, $nick, $address) = @_;
    return unless $server->{tag} eq Irssi::settings_get_str('amythest_server_tag');
    return unless ($nick // '') eq 'amy.local';
    return unless $data =~ /^\S+ (#amy-[a-f0-9]{12}) ([A-Za-z0-9-]+) :Amythest focus$/;
    my $channel = $1;
    my $item = $server->channel_find($channel);
    if ($item) {
        $item->set_active();
        $item->window()->set_active();
    }
    Irssi::signal_stop();
}
Irssi::signal_add('event 290', 'focus_message');

Irssi::settings_add_str('amythest', 'amythest_host', '127.0.0.1');
Irssi::settings_add_int('amythest', 'amythest_port', 6669);
my $state = $ENV{AMYTHEST_MESSAGES_STATE} // (($ENV{XDG_STATE_HOME} // "$ENV{HOME}/.local/state") . '/amythest-messages-poc');
Irssi::settings_add_str('amythest', 'amythest_token_file', "$state/owner.token");
sub connect_amythest {
    my $host = Irssi::settings_get_str('amythest_host');
    my $port = Irssi::settings_get_int('amythest_port');
    unless ($host eq '127.0.0.1' && $port > 0 && $port < 65536) {
        Irssi::print('Use a local SSH tunnel at 127.0.0.1 for the messaging POC.'); return;
    }
    my $path = Irssi::settings_get_str('amythest_token_file');
    open my $fh, '<', $path or do { Irssi::print('Cannot read the messaging owner token. Start amy messages serve -demo first.'); return; };
    my $token = <$fh> // ''; close $fh; chomp $token;
    unless ($token =~ /^[a-f0-9]{64}$/) { Irssi::print('Invalid messaging token file.'); return; }
    my $tag = Irssi::settings_get_str('amythest_server_tag');
    unless ($tag =~ /^[A-Za-z0-9_-]+$/) { Irssi::print('Invalid messaging server tag.'); return; }
    Irssi::command("NETWORK ADD $tag");
    Irssi::command("CONNECT -network $tag $host $port $token");
}
Irssi::command_bind('amy_connect', 'connect_amythest');
