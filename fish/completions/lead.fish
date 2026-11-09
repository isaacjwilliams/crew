complete -c lead -l project -x -d 'Work on this project (all: every project)' \
    -a "(path basename (crew config CREW_HOME 2>/dev/null)/projects/*/ 2>/dev/null) all"
