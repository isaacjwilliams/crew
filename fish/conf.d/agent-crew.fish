# agent crew: the crew home and `crew` on PATH.
set -q CREW_HOME; or set -gx CREW_HOME $HOME/crew
if test -d $CREW_HOME/bin; and not contains -- $CREW_HOME/bin $PATH
    set -gx PATH $CREW_HOME/bin $PATH
end
